Output test-full.gif

Set Shell "bash"
Set Width 1200
Set Height 700

# Start host in background on a fixed port
Type "./parley host --topic 'Go best practices' --port 9999 &"
Enter
Sleep 3s

# Join as an agent (using echo as a fake agent for testing)
Type "./parley join --port 9999 --name TestBot --role 'test agent' -- echo hello"
Enter
Sleep 3s
