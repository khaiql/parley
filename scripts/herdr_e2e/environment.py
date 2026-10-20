"""Owned processes and isolated controller/participant environments."""

import hashlib
import json
import os
from pathlib import Path
import pty
import pwd
import re
import select
import shlex
import shutil
import signal
import socket
import subprocess
import tempfile
import time
from .proxy import SocketProxy

REPO = Path(__file__).resolve().parents[2]
REMINDER = "Room messages are pending. Use your Parley skill"


class NativeError(RuntimeError):
    def __init__(self, response):
        self.code = response["error"]["code"]
        super().__init__(str(response))


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def eventually(predicate, description, timeout=15):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        try:
            result = predicate()
            if result:
                return result
        except (OSError, ValueError, KeyError) as error:
            last = error
        except NativeError as error:
            if error.code != "agent_not_found":
                raise
            last = error
        time.sleep(0.15)
    raise AssertionError(f"Timed out: {description}; last error: {last}")


def run(argv, env=None, data=None, timeout=40, check=True, cwd=None):
    result = subprocess.run([str(a) for a in argv], input=data, text=True,
                            capture_output=True, env=env, timeout=timeout, cwd=cwd)
    if check and result.returncode:
        raise AssertionError(f"{argv}: {result.returncode}\n{result.stdout}\n{result.stderr}")
    return result


def private_write(path, text, executable=False):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    descriptor, temporary = tempfile.mkstemp(prefix="." + path.name, dir=path.parent)
    try:
        with os.fdopen(descriptor, "w") as stream:
            stream.write(text)
        os.chmod(temporary, 0o700 if executable else 0o600)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def descendants(pid):
    rows = run(["ps", "-axo", "pid=,ppid="]).stdout.splitlines()
    parents = {int(row.split()[0]): int(row.split()[1]) for row in rows}
    owned = {pid}
    while True:
        children = {child for child, parent in parents.items() if parent in owned}
        if children <= owned:
            return owned
        owned |= children


def terminate(process):
    if process and process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=8)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)


class Host:
    def __init__(self, lab, name):
        self.lab, self.name = lab, name
        self.root = lab.root / name
        self.root.mkdir(mode=0o700)
        self.config = self.root / "herdr.toml"
        private_write(self.config, 'onboarding=false\n[update]\nversion_check=false\n'
                      'manifest_check=false\n[terminal]\ndefault_shell="/bin/sh"\n'
                      'shell_mode="non_login"\n[session]\nresume_agents_on_restore=false\n')
        self.env = {k: v for k, v in os.environ.items()
                    if not k.startswith(("HERDR_", "PARLEY_", "SSH_"))}
        self.env.update(HERDR_CONFIG_PATH=str(self.config), HERDR_SESSION="t", HERDR_ENV="1",
                        XDG_CONFIG_HOME=str(self.root / "c"), XDG_STATE_HOME=str(self.root / "s"),
                        PARLEY_STATE_DIR=str(lab.root / name[0]),
                        PATH=str(lab.root / "bin") + os.pathsep + os.environ["PATH"])
        self.process = self.broker = self.proxy = None
        self.socket = self.coordinator_socket = None
        self.agents = []

    def start(self):
        self.process = self.lab.spawn([self.lab.herdr, "--session", "t", "server"],
                                      self.env, self.root / "herdr.log")
        def inventory():
            require(self.process.poll() is None, f"{self.name} Herdr exited")
            result = self.herdr("session", "list", "--json")
            return next((x["socket_path"] for x in result["sessions"]
                         if x["name"] == "t" and x["running"]), None)
        self.socket = str(Path(eventually(inventory, f"{self.name} native server")).resolve())
        self.env["HERDR_SOCKET_PATH"] = self.socket
        self.key = hashlib.sha256(self.socket.encode()).hexdigest()
        self.coordinator_socket = Path("/tmp") / f"parley-h-{os.getuid()}-{self.key}" / "broker"
        self.store = self.root / "s/herdr/plugins/parley/sessions" / self.key
        self.proxy = SocketProxy(self)
        return self

    def command(self, args, **kwargs):
        return run(args, env=self.env, **kwargs)

    def herdr(self, *args):
        return json.loads(self.command([self.lab.herdr, "--session", "t", *args]).stdout)

    def parley(self, *args, **kwargs):
        return json.loads(self.command([self.lab.binary, *args], **kwargs).stdout)

    def api(self, method, params=None):
        with socket.socket(socket.AF_UNIX) as connection:
            connection.settimeout(5)
            connection.connect(self.socket)
            connection.sendall((json.dumps({"id": "e2e", "method": method,
                                            "params": params or {}}) + "\n").encode())
            with connection.makefile() as stream:
                response = json.loads(stream.readline())
        self.lab.record("api", host=self.name, method=method, response=response)
        if "error" in response:
            raise NativeError(response)
        return response["result"]

    def link(self):
        self.herdr("plugin", "link", str(self.lab.bundle))
        plugins = self.herdr("plugin", "list", "--json")["result"]["plugins"]
        require(any(p["plugin_id"] == "parley" and p["plugin_root"] == str(self.lab.bundle)
                    for p in plugins), "test bundle is not linked in the isolated session")

    def status(self):
        return self.parley("herdr", "status")

    def begin_coordinator(self):
        # Setup's normal detached coordinator is stopped via owned process discovery below.
        self.parley("herdr", "setup")
        self.lab.stop_brokers()
        self.restart_coordinator()

    def restart_coordinator(self):
        terminate(self.broker)
        self.broker = self.lab.spawn([self.lab.binary, "herdr", "broker", "--native-session", "t"],
                                     self.env, self.root / "coordinator.log")
        eventually(self.ipc_live, "coordinator IPC")

    def ipc_live(self):
        if self.broker.poll() is not None:
            raise AssertionError("owned coordinator exited before readiness")
        try:
            with socket.socket(socket.AF_UNIX) as connection:
                connection.settimeout(.3)
                connection.connect(str(self.coordinator_socket))
            return True
        except OSError:
            return False

    def notification(self, binding):
        for path in (self.store / "notifications/status").glob("*.json"):
            for value in json.loads(path.read_text()).get("bindings") or []:
                if value["session_id"] == binding["session_id"]:
                    value["observed_file_mtime_ns"] = path.stat().st_mtime_ns
                    return value

    def attempts(self):
        return [json.loads(p.read_text()) for p in (self.store / "notifications/attempts").glob("*.json")]

    def signal(self, agent):
        self.api("workspace.report_metadata", dict(workspace_id=agent.workspace, source="plugin:parley",
                 tokens={"parley_request": str(time.time_ns())}))

    def form(self, agent, op, value, manual=False):
        env = {**self.env, "PARLEY_ACTION": op, "PARLEY_TARGET_JSON": json.dumps(agent.target())}
        master, slave = pty.openpty()
        process = subprocess.Popen([str(self.lab.binary), "herdr", "form"], env=env,
                                   stdin=slave, stdout=slave, stderr=slave, close_fds=True)
        os.close(slave)
        output = bytearray()
        deadline = time.monotonic() + 55
        submitted = closed = manual_selected = False
        try:
            while time.monotonic() < deadline:
                if select.select([master], [], [], .1)[0]:
                    try:
                        part = os.read(master, 65536)
                    except OSError:
                        break
                    if not part:
                        break
                    output.extend(part)
                text = output.decode(errors="replace")
                prompt = "Topic (Enter" if op == "start" else "M: enter room ID"
                if not submitted and prompt in text:
                    if manual and not manual_selected:
                        os.write(master, b"m\n")
                        manual_selected = True
                    elif not manual:
                        os.write(master, (value + "\n").encode())
                        submitted = True
                if manual_selected and not submitted and "Room ID" in text:
                    os.write(master, (value + "\n").encode())
                    submitted = True
                if not closed and ("Agent instructions:" in text or "Press Enter to close" in text):
                    os.write(master, b"\n")
                    closed = True
                if process.poll() is not None:
                    break
            else:
                raise AssertionError(f"form timeout: {output.decode(errors='replace')}")
            process.wait(timeout=5)
        finally:
            terminate(process)
            os.close(master)
            private_write(self.root / f"form-{time.time_ns()}.log", output.decode(errors="replace"))
        require(process.returncode == 0, f"{op} form failed: {output.decode(errors='replace')}")
        bindings = self.status()["bindings"]
        session = re.search(r"Session:\s+(psn_[a-z0-9]+)", output.decode(errors="replace"))
        require(session, f"form has no participant session: {output.decode(errors='replace')}")
        matches = [b for b in bindings.values() if b["target"]["terminal_id"] == agent.terminal
                   and b["session_id"] == session.group(1)]
        require(len(matches) == 1, f"form did not produce one original binding: {matches}")
        binding = matches[0]
        self.last_form = output.decode(errors="replace")
        self.lab.record("form", host=self.name, op=op, binding=binding,
                        output=output.decode(errors="replace"))
        return binding


class Agent:
    def __init__(self, host, name, native=None, kind="claude", state="idle"):
        self.host, self.name = host, name
        self.kind = kind
        self.native = native or "conversation-" + name
        self.identity_reported = False
        self.log = host.root / f"{name}.input.jsonl"
        self.state_file = host.root / f"{name}.state"
        private_write(self.state_file, state)
        pane = host.api("workspace.create", {"cwd": str(host.root), "focus": False})["root_pane"]
        self.pane, self.terminal = pane["pane_id"], pane["terminal_id"]
        self.workspace = pane["workspace_id"]
        host.command([host.lab.herdr, "pane", "run", self.pane,
                      "exec " + shlex.join([str(host.lab.agent_binary.with_name(kind)), str(self.log), str(self.state_file)])])
        eventually(lambda: self.log.exists(), f"{name} foreground recorder")
        eventually(lambda: self.info(), f"{name} native process detection")
        self.report_identity(self.native)
        self.state(state)
        host.agents.append(self)

    def target(self):
        return dict(machine_id="self", herdr_socket=self.host.socket, pane_id=self.pane,
                    workspace_id=self.workspace, terminal_id=self.terminal, kind=self.kind, native_session="")

    def info(self):
        return self.host.api("agent.get", {"target": self.pane})["agent"]

    def report_identity(self, native):
        self.host.api("pane.report_agent_session", dict(pane_id=self.pane, source="herdr:" + self.kind,
                      agent=self.kind, agent_session_id=native,
                      session_start_source="clear" if self.identity_reported else "startup"))
        eventually(lambda: self.info().get("agent_session", {}).get("value") == native,
                   f"{self.name} native identity")
        self.identity_reported = True

    def state(self, state):
        private_write(self.state_file, state)
        expected = {"idle", "done"} if state == "idle" else {state}
        return eventually(lambda: self.info() if self.info()["agent_status"] in expected else None,
                          f"{self.name} native {state}")

    def move(self):
        moved = self.host.api("pane.move", dict(pane_id=self.pane, focus=False,
                              destination={"type": "new_workspace", "label": "moved-e2e"}))
        pane = moved["move_result"]["pane"]
        self.pane, self.workspace = pane["pane_id"], pane["workspace_id"]
        require(pane["terminal_id"] == self.terminal, "pane move replaced the terminal")
        return self.info()

    def reminders(self):
        if not self.log.exists():
            return []
        return [json.loads(line) for line in self.log.read_text().splitlines()
                if line and REMINDER in json.loads(line)["text"]]


class Lab:
    def __init__(self, root=None):
        require(root is None or not Path(root).exists(), "--output must name a new directory")
        self.root = Path(root or tempfile.mkdtemp(prefix="ph-e2e-", dir="/tmp")).resolve()
        self.root.mkdir(exist_ok=True, mode=0o700)
        require(len(str(self.root)) <= 30, "Use a shorter --output path for Unix socket limits")
        require(str(self.root).isascii() and all(c.isalnum() or c in "/._-" for c in str(self.root)),
                "--output must not contain spaces or shell/config punctuation")
        self.herdr = shutil.which("herdr")
        self.ssh = shutil.which("ssh")
        self.sshd = shutil.which("sshd") or "/usr/sbin/sshd"
        require(self.herdr and self.ssh and Path(self.sshd).exists(), "herdr, ssh and sshd are required")
        self.processes, self.hosts = [], []
        self.ssh_process = None
        self.bundle = self.root / "bundle"
        self.binary = self.bundle / "bin/parley"
        self.agent_binary = self.bundle / "bin/claude"
        self.bundle.mkdir(mode=0o700)
        self.binary.parent.mkdir(mode=0o700)
        shutil.copy(REPO / "herdr-plugin.toml", self.bundle)
        shutil.copytree(REPO / "skills", self.bundle / "skills")
        run(["go", "build", "-o", self.binary, "./cmd/parley"], timeout=120, cwd=REPO)
        run(["go", "build", "-o", self.agent_binary, "./scripts/herdr_e2e/agent"], timeout=120, cwd=REPO)
        shutil.copy(self.agent_binary, self.agent_binary.with_name("codex"))
        self.record("versions", herdr=run([self.herdr, "--version"]).stdout.strip(),
                    ssh=run([self.ssh, "-V"]).stderr.strip(),
                    revision=run(["git", "rev-parse", "HEAD"]).stdout.strip())

    def record(self, event, **fields):
        with (self.root / "evidence.jsonl").open("a") as out:
            out.write(json.dumps(dict(event=event, monotonic=time.monotonic(), **fields)) + "\n")

    def spawn(self, argv, env, log):
        stream = log.open("a")
        try:
            process = subprocess.Popen([str(a) for a in argv], env=env, stdout=stream,
                                       stderr=stream, start_new_session=True)
        finally:
            stream.close()
        self.processes.append(process)
        return process

    def host(self, name):
        host = Host(self, name)
        self.hosts.append(host)
        return host.start()

    def ssh_setup(self, remote):
        self.remote = remote
        key = self.root / "key"
        host_key = self.root / "host-key"
        for path in [key, host_key]:
            run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", path])
        private_write(self.root / "authorized_keys", key.with_suffix(".pub").read_text())
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            self.port = listener.getsockname()[1]
        self.target = f"ssh://{pwd.getpwuid(os.getuid()).pw_name}@127.0.0.1:{self.port}"
        public = host_key.with_suffix(".pub").read_text().split()
        private_write(self.root / "known_hosts", f"[127.0.0.1]:{self.port} {public[0]} {public[1]}\n")
        self.ssh_config = self.root / "ssh_config"
        private_write(self.ssh_config, f"Host *\n IdentityFile {key}\n IdentitiesOnly yes\n"
                      f" UserKnownHostsFile {self.root}/known_hosts\n StrictHostKeyChecking yes\n"
                      " ConnectTimeout 2\n ServerAliveInterval 1\n ServerAliveCountMax 2\n")
        # Inject only an isolated config; every argument still reaches the real OpenSSH client.
        private_write(self.root / "bin/ssh", "#!/bin/sh\nexec " + shlex.join(
                      [self.ssh, "-F", str(self.ssh_config), "-o", f"IdentityFile={key}",
                       "-o", "IdentitiesOnly=yes", "-o", f"UserKnownHostsFile={self.root}/known_hosts",
                       "-o", "StrictHostKeyChecking=yes"]) + ' "$@"\n', True)
        remote_env = {k: v for k, v in remote.env.items()
                      if k in {"PATH", "HERDR_CONFIG_PATH", "HERDR_SESSION", "HERDR_ENV",
                               "XDG_CONFIG_HOME", "XDG_STATE_HOME", "PARLEY_STATE_DIR", "HERDR_SOCKET_PATH"}}
        command_log = self.root / "remote-commands.log"
        private_write(self.root / "remote-entry", "#!/bin/sh\nprintf '%s\\n' \"$SSH_ORIGINAL_COMMAND\" >> "
                      + shlex.quote(str(command_log)) + "\nexec env "
                      + shlex.join([f"{k}={v}" for k, v in remote_env.items()])
                      + ' /bin/sh -c "$SSH_ORIGINAL_COMMAND"\n', True)
        self.sshd_config = self.root / "sshd_config"
        private_write(self.sshd_config, f"Port {self.port}\nListenAddress 127.0.0.1\nHostKey {host_key}\n"
                      f"PidFile {self.root}/sshd.pid\nAuthorizedKeysFile {self.root}/authorized_keys\n"
                      "StrictModes no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\n"
                      "UsePAM no\nAllowTcpForwarding yes\nGatewayPorts no\nPermitUserEnvironment no\n"
                      f"AllowUsers {pwd.getpwuid(os.getuid()).pw_name}\nLogLevel VERBOSE\n"
                      f"ForceCommand {self.root}/remote-entry\n")
        self.ssh_start()
        result = run([self.ssh, "-F", self.ssh_config, self.target, "printf e2e-ssh-ready"])
        require(result.stdout == "e2e-ssh-ready", "actual SSH execution failed")

    def ssh_start(self):
        self.ssh_process = self.spawn([self.sshd, "-D", "-e", "-f", self.sshd_config],
                                      os.environ.copy(), self.root / "sshd.log")
        eventually(lambda: self.ssh_process.poll() is None and self._ssh_listening(), "OpenSSH listener")

    def _ssh_listening(self):
        try:
            with socket.create_connection(("127.0.0.1", self.port), timeout=.2):
                return True
        except OSError:
            return False

    def disconnect(self):
        owned = descendants(self.ssh_process.pid)
        terminate(self.ssh_process)
        for pid in sorted(owned - {self.ssh_process.pid}, reverse=True):
            try:
                os.kill(pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
        self.record("ssh_disconnected", owned_pids=sorted(owned))

    def stop_brokers(self):
        self._stop_binary("herdr broker")
        for host in self.hosts:
            eventually(lambda: not host.coordinator_socket.exists(), "owned broker shutdown")

    def _stop_binary(self, arguments=None):
        for line in run(["ps", "-axo", "pid=,command="]).stdout.splitlines():
            pid, _, command = line.strip().partition(" ")
            if command.startswith(str(self.binary) + " ") and (arguments is None or arguments in command):
                try:
                    os.kill(int(pid), signal.SIGTERM)
                except ProcessLookupError:
                    pass

    def interrupt_remote_helpers(self):
        children = descendants(self.ssh_process.pid)
        killed = []
        for line in run(["ps", "-axo", "pid=,command="]).stdout.splitlines():
            pid, _, command = line.strip().partition(" ")
            if int(pid) in children and command.startswith(str(self.binary) + " herdr rpc"):
                os.kill(int(pid), signal.SIGKILL)
                killed.append(int(pid))
        require(killed, "No real SSH helper processes found to interrupt")
        self.record("remote_helpers_interrupted", pids=killed)

    def stop_host_adapters(self, host):
        names = {json.loads(p.read_text())["name"] for p in
                 (Path(host.env["PARLEY_STATE_DIR"]) / "sessions").glob("*.json")}
        killed = []
        for line in run(["ps", "-axo", "pid=,command="]).stdout.splitlines():
            pid, _, command = line.strip().partition(" ")
            if not command.startswith(str(self.binary) + " "):
                continue
            args = shlex.split(command)
            if len(args) > 1 and args[1] in {"__room-daemon", "__participant-daemon"} and "--name" in args:
                if args[args.index("--name") + 1] in names:
                    try:
                        os.kill(int(pid), signal.SIGTERM)
                        killed.append(int(pid))
                    except ProcessLookupError:
                        pass
        self.record("host_parley_processes_stopped", host=host.name, pids=killed)

    def close(self):
        self._stop_binary("herdr broker")
        self._stop_binary()
        for process in reversed(self.processes):
            terminate(process)
        # A deliberately killed coordinator cannot close its detached SSH masters.
        prefix = self.ssh + " -F " + str(self.ssh_config) + " " if hasattr(self, "ssh_config") else None
        if prefix:
            for line in run(["ps", "-axo", "pid=,command="]).stdout.splitlines():
                pid, _, command = line.strip().partition(" ")
                if command.startswith(prefix):
                    try:
                        os.kill(int(pid), signal.SIGTERM)
                    except ProcessLookupError:
                        pass
        for host in self.hosts:
            if host.proxy:
                host.proxy.close()
            if host.coordinator_socket:
                shutil.rmtree(host.coordinator_socket.parent, ignore_errors=True)
        self.record("cleanup_complete")
