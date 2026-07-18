# Resources for Agent Sandbox Research


## Providers 

- **Anthropic**
  - https://code.claude.com/docs/en/sandbox-environments- 
- **OpenAI**
  - https://developers.openai.com/api/docs/guides/agents/sandboxes
- **gVisor**
  - https://gvisor.dev/ 
- **Firecracker**
  - https://firecracker-microvm.github.io/
- **Kubernetes**
  - https://kubernetes.io/blog/2026/03/20/running-agents-on-kubernetes-with-agent-sandbox/
  - https://github.com/kubernetes-sigs/agent-sandbox
  - https://agent-sandbox.sigs.k8s.io/
- **SmolVM**
- **Microsandbox**
- **OpenSandbox**
- **E2B**
- **Docker Sanboxes**
  - https://www.docker.com/products/docker-sandboxes/


## Dimensions 

- **Isolation**: The degree to which the sandbox isolates the agent from the host system and other agents. This can include process isolation, network isolation, and filesystem isolation.
  - **Networking**: The ability to control and restrict network access for the agent, including the ability to block or allow specific domains, IP addresses, or protocol coverage. MiTM, HTTP path rules, HTTP GET rules, WSS support, Regex support. Etc 
- **Configuration and Setup**
  - Copying from host to sandbox
  - Setting up env 
  - Ease 
  - Depth 
- **DX (Developer Experience)**: The ease of use and developer-friendliness of the sandbox. 
  - Browser open on host 
  - Credential forwarding (ssh, pgp, git, etc)
- **Extensibility**: The ability to extend or customize the sandbox environment to suit specific needs. This can include adding new tools, libraries, or configurations.
- **Monitoring and Logging**: The ability to monitor the agent's activity and log its actions for debugging and auditing purposes.
- **Performance**: The impact of the sandbox on the agent's performance, including any overhead introduced by the isolation mechanisms.
- **Security**: The security features of the sandbox, including protection against malicious agents, vulnerabilities, and attacks. This can include features like resource limits, access controls, and secure communication channels.
- **Architecture**: The underlying architecture of the sandbox, including the technologies and frameworks used to implement it. This can include containerization, virtualization, or other isolation techniques. ig Supervisor, Hypervisor, Controlplane, Dataplane, etc. VM vs Container, benetfits etc. 
