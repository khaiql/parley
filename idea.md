# A TUI based group chat application.

## What

I want to create a chat room terminal based application, where everyone in the room is like peer, they may have different roles and expertise, but they are in the room, chatting to each other. 

Each participant in the room is a claude code, codex, or opencode, basically a coding agent. 

The workflow is as follows:
1. I create a new room, let's say `group_chat --topic "let's build a new claude code"`. It shows me a port for other agents to join the room
2. I spin up a new agent with claude code in a different terminal `claude`. Ask it to use the group chat skill to join a room, specify their name, the folder, and the role. Role could be specified by user, or infer from the codebase. 
3. I spin up another agent, similar to step 2, but different repo. 

Now I chat in my main TUI. The app basically functions like a normal chat room, one sends a message, other sees, they can send message to add to the conversation, challenge each other, compliment each other, debate with each other, ask for help, etc... everything that a group of people can do and might do. 

The agent is on a separate terminal, so they can do whatever they can in their session, they can think, they can browse file, spin up subagent, edit file, etc... Their response is broadcasted to the room and other can see. 

## Architecture

A server is spin up with an identity and a port. 
Each agent has access to a skill, and resources that allow them to interact with each other, such as join the room, send a message, retrieve previous messages (if they join later), etc...

## Open Questions

- How does the communication protocol look like? Like if one sends a message to the group, both receive the message, they start thinking, they may or may not respond to that message. I don't want all agents in the room to respond to every message, that would be too noisy. They should think, and decide if they want to contribute to the conversation, or not. If they do, they send a message to the group. 
- The group needs to be productive, again, the whole point is that, a topic is being discussed, they spar ideas, they arrange work. In some other cases, maybe it's just two agent pair programming. How do we ensure that? 
- Technology, i'm thinking of using Go, and TCP for communications. I expect the tool to run locally only. 
- What are some of the edge cases that I haven't thought about? 

## Goal

- Let's spar on the idea, is it technically feasible
- Let's build a PoC
- Once we have a working poc, we need to plan it code it properly. 