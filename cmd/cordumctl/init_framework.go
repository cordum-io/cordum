package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

var validFrameworks = map[string]bool{
	"langchain": true,
	"crewai":    true,
	"autogen":   true,
}

func validateFramework(name string) error {
	if name == "" {
		return nil
	}
	if !validFrameworks[strings.ToLower(name)] {
		return fmt.Errorf("unsupported framework %q: choose langchain, crewai, or autogen", name)
	}
	return nil
}

func scaffoldFramework(target, framework string, force bool) error {
	framework = strings.ToLower(framework)
	switch framework {
	case "langchain":
		return scaffoldLangchain(target, force)
	case "crewai":
		return scaffoldCrewAI(target, force)
	case "autogen":
		return scaffoldAutoGen(target, force)
	default:
		return fmt.Errorf("unknown framework: %s", framework)
	}
}

// scaffoldLangchain generates a LangGraph project with Cordum governance.
func scaffoldLangchain(target string, force bool) error {
	// Worker files are new — use caller's force preference.
	workerFiles := map[string]string{
		filepath.Join(target, "worker", "agent.py"):         langchainAgentPy,
		filepath.Join(target, "worker", "requirements.txt"): langchainRequirements,
		filepath.Join(target, "worker", "Dockerfile"):       workerDockerfile("requirements.txt", "agent.py"),
	}
	for path, content := range workerFiles {
		if err := writeFile(path, content, force); err != nil {
			return err
		}
	}
	// Safety policy intentionally replaces the base template from scaffoldInit.
	if err := writeFileOverwrite(filepath.Join(target, "config", "safety.yaml"), langchainSafetyYAML); err != nil {
		return err
	}
	return patchComposeWithWorker(target, "cordum-langchain-worker")
}

// scaffoldCrewAI generates a CrewAI project with Cordum safety gates.
func scaffoldCrewAI(target string, force bool) error {
	workerFiles := map[string]string{
		filepath.Join(target, "worker", "crew.py"):          crewaiCrewPy,
		filepath.Join(target, "worker", "requirements.txt"): crewaiRequirements,
		filepath.Join(target, "worker", "Dockerfile"):       workerDockerfile("requirements.txt", "crew.py"),
	}
	for path, content := range workerFiles {
		if err := writeFile(path, content, force); err != nil {
			return err
		}
	}
	if err := writeFileOverwrite(filepath.Join(target, "config", "safety.yaml"), crewaiSafetyYAML); err != nil {
		return err
	}
	return patchComposeWithWorker(target, "cordum-crewai-worker")
}

// scaffoldAutoGen generates an AutoGen multi-agent project with Cordum governance.
func scaffoldAutoGen(target string, force bool) error {
	workerFiles := map[string]string{
		filepath.Join(target, "worker", "agents.py"):        autogenAgentsPy,
		filepath.Join(target, "worker", "requirements.txt"): autogenRequirements,
		filepath.Join(target, "worker", "Dockerfile"):       workerDockerfile("requirements.txt", "agents.py"),
	}
	for path, content := range workerFiles {
		if err := writeFile(path, content, force); err != nil {
			return err
		}
	}
	if err := writeFileOverwrite(filepath.Join(target, "config", "safety.yaml"), autogenSafetyYAML); err != nil {
		return err
	}
	return patchComposeWithWorker(target, "cordum-autogen-worker")
}

// patchComposeWithWorker appends the framework worker service to docker-compose.yml.
func patchComposeWithWorker(target, serviceName string) error {
	workerService := fmt.Sprintf(`
  %s:
    build:
      context: ./worker
    depends_on:
      nats:
        condition: service_healthy
      redis:
        condition: service_healthy
      cordum-api-gateway:
        condition: service_healthy
    environment:
      - NATS_URL=nats://nats:4222
      - REDIS_URL=redis://:${REDIS_PASSWORD:-cordum-dev}@redis:6379
      - CORDUM_WORKER_ID=%s
      - CORDUM_POOL=default
    restart: unless-stopped
`, serviceName, serviceName)

	composePath := filepath.Join(target, "docker-compose.yml")
	return appendToFile(composePath, workerService)
}

func workerDockerfile(reqFile, entrypoint string) string {
	return fmt.Sprintf(`FROM python:3.12-slim

WORKDIR /app

COPY %s .
RUN pip install --no-cache-dir -r %s

COPY . .

CMD ["python", "%s"]
`, reqFile, reqFile, entrypoint)
}

// appendToFile appends content just before the "volumes:" line in an existing file.
// If "volumes:" isn't found, the content is appended at the end.
func appendToFile(path, content string) error {
	data, err := readFileBytes(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	existing := string(data)

	// Insert before the "volumes:" section so the service is part of "services:".
	volIdx := strings.Index(existing, "\nvolumes:")
	if volIdx >= 0 {
		existing = existing[:volIdx] + content + existing[volIdx:]
	} else {
		existing += content
	}
	return writeFileOverwrite(path, existing)
}

func readFileBytes(path string) ([]byte, error) {
	// #nosec G304 -- CLI explicitly reads local files.
	return readFileRaw(path)
}

// ---------- LangGraph templates ----------

const langchainAgentPy = `"""LangGraph agent governed by Cordum.

This worker connects to Cordum via NATS, receives job requests, and executes
a LangGraph agent with tool calls. Cordum's Safety Kernel evaluates each job
before the agent runs — denied jobs never reach your agent code.
"""
import asyncio
import json
import os
from typing import Any

from cap.runtime import Agent, Context

# ---- LangGraph agent definition ----
# Replace this with your real LangGraph agent. This example shows the
# integration pattern: Cordum dispatches jobs, your agent processes them.

try:
    from langgraph.graph import StateGraph, END
    from langchain_core.messages import HumanMessage, AIMessage

    class AgentState(dict):
        """Minimal state for the research agent."""
        pass

    def research_node(state: AgentState) -> AgentState:
        """Simulates a research step — replace with your real LLM call."""
        query = state.get("query", "")
        state["result"] = f"Research complete for: {query}"
        state["sources"] = ["internal-docs", "approved-apis"]
        return state

    def review_node(state: AgentState) -> AgentState:
        """Reviews research output for compliance."""
        state["reviewed"] = True
        state["compliance_status"] = "passed"
        return state

    # Build the graph
    graph = StateGraph(AgentState)
    graph.add_node("research", research_node)
    graph.add_node("review", review_node)
    graph.set_entry_point("research")
    graph.add_edge("research", "review")
    graph.add_edge("review", END)
    app = graph.compile()

    LANGGRAPH_AVAILABLE = True
except ImportError:
    LANGGRAPH_AVAILABLE = False
    app = None

# ---- Cordum worker ----

agent = Agent(
    sender_id=os.getenv("CORDUM_WORKER_ID", "langchain-worker"),
    pool=os.getenv("CORDUM_POOL", "default"),
)

@agent.job("job.default")
async def handle_research(ctx: Context, input: Any) -> dict:
    """Handle a research job dispatched by Cordum.

    The Safety Kernel has already evaluated this job's policy before it
    reaches this handler. Denied jobs never arrive here.
    """
    query = ""
    if isinstance(input, dict):
        query = input.get("query", input.get("prompt", ""))
    elif isinstance(input, str):
        query = input

    if not query:
        return {"error": "No query provided. Send {\"query\": \"your question\"}"}

    ctx.logger.info("Processing research query", extra={"query": query[:100]})

    if LANGGRAPH_AVAILABLE and app is not None:
        result = app.invoke({"query": query})
        return {
            "answer": result.get("result", ""),
            "sources": result.get("sources", []),
            "reviewed": result.get("reviewed", False),
        }

    # Fallback when langgraph is not installed (e.g., testing the scaffold).
    return {
        "answer": f"Processed: {query}",
        "note": "Install langgraph for full agent functionality",
    }

if __name__ == "__main__":
    asyncio.run(agent.run())
`

const langchainRequirements = `# Cordum Agent Protocol SDK
cap-sdk>=2.8.0

# LangGraph + LangChain
langgraph>=0.2.0
langchain-core>=0.3.0

# NATS client
nats-py>=2.9.0

# Protobuf (required by CAP)
protobuf>=4.25.0
`

const langchainSafetyYAML = `# Safety policy for LangGraph agent
# Cordum evaluates every job against these rules before dispatching to your agent.
default_decision: deny
output_policy:
  enabled: true
  fail_mode: closed
default_tenant: default
tenants:
  default:
    allow_topics:
      - "job.default"
    deny_topics:
      - "sys.*"
    allowed_repo_hosts: []
    denied_repo_hosts: []
    mcp:
      allow_servers: []
      deny_servers: []
      allow_tools: []
      deny_tools: []
      allow_resources: []
      deny_resources: []
      allow_actions: []
      deny_actions: []
rules:
  - id: allow-research
    match:
      topics: ["job.default"]
    decision: allow
input_rules:
  - id: deny-pii-queries
    severity: high
    match:
      topics: ["job.default"]
      scanners: ["pii"]
    decision: deny
    reason: "Query contains PII (SSN or credit card number)"
`

// ---------- CrewAI templates ----------

const crewaiCrewPy = `"""CrewAI crew with Cordum safety gates.

This worker connects to Cordum via NATS, receives job requests, and executes
a CrewAI crew with safety gates. Cordum's Safety Kernel enforces policies on
every job — PII detection, approval workflows, and content scanning happen
before your crew processes any data.
"""
import asyncio
import json
import os
from typing import Any

from cap.runtime import Agent, Context

# ---- CrewAI crew definition ----
# Replace this with your real CrewAI crew. This example shows the integration
# pattern with Cordum governance.

try:
    from crewai import Agent as CrewAgent, Task, Crew, Process

    researcher = CrewAgent(
        role="Research Analyst",
        goal="Find accurate information about the given topic",
        backstory="You are a thorough research analyst who verifies facts.",
        verbose=False,
        allow_delegation=False,
    )

    writer = CrewAgent(
        role="Content Writer",
        goal="Write a clear, concise summary of the research findings",
        backstory="You are a skilled writer who creates readable summaries.",
        verbose=False,
        allow_delegation=False,
    )

    CREWAI_AVAILABLE = True
except ImportError:
    CREWAI_AVAILABLE = False

# ---- Cordum worker ----

agent = Agent(
    sender_id=os.getenv("CORDUM_WORKER_ID", "crewai-worker"),
    pool=os.getenv("CORDUM_POOL", "default"),
)

@agent.job("job.default")
async def handle_crew_task(ctx: Context, input: Any) -> dict:
    """Handle a crew task dispatched by Cordum.

    The Safety Kernel has already evaluated this job's policy before it
    reaches this handler — PII scanning and approval gates are enforced
    at the Cordum layer, not in your crew code.
    """
    prompt = ""
    if isinstance(input, dict):
        prompt = input.get("prompt", input.get("query", ""))
    elif isinstance(input, str):
        prompt = input

    if not prompt:
        return {"error": "No prompt provided. Send {\"prompt\": \"your task\"}"}

    ctx.logger.info("Processing crew task", extra={"prompt": prompt[:100]})

    if CREWAI_AVAILABLE:
        research_task = Task(
            description=f"Research the following topic: {prompt}",
            expected_output="A detailed factual summary with key findings",
            agent=researcher,
        )
        write_task = Task(
            description="Write a concise summary of the research findings",
            expected_output="A clear, readable summary under 200 words",
            agent=writer,
        )
        crew = Crew(
            agents=[researcher, writer],
            tasks=[research_task, write_task],
            process=Process.sequential,
            verbose=False,
        )
        result = crew.kickoff()
        return {
            "summary": str(result),
            "agents_used": ["Research Analyst", "Content Writer"],
            "process": "sequential",
        }

    # Fallback when crewai is not installed.
    return {
        "summary": f"Processed: {prompt}",
        "note": "Install crewai for full crew functionality",
    }

if __name__ == "__main__":
    asyncio.run(agent.run())
`

const crewaiRequirements = `# Cordum Agent Protocol SDK
cap-sdk>=2.8.0

# CrewAI
crewai>=0.80.0

# NATS client
nats-py>=2.9.0

# Protobuf (required by CAP)
protobuf>=4.25.0
`

const crewaiSafetyYAML = `# Safety policy for CrewAI crew
# Cordum evaluates every job before your crew processes it.
# PII detection and approval gates are enforced here — your crew code
# only sees jobs that pass policy checks.
default_decision: deny
output_policy:
  enabled: true
  fail_mode: closed
default_tenant: default
tenants:
  default:
    allow_topics:
      - "job.default"
    deny_topics:
      - "sys.*"
    allowed_repo_hosts: []
    denied_repo_hosts: []
    mcp:
      allow_servers: []
      deny_servers: []
      allow_tools: []
      deny_tools: []
      allow_resources: []
      deny_resources: []
      allow_actions: []
      deny_actions: []
rules:
  - id: allow-crew-tasks
    match:
      topics: ["job.default"]
    decision: allow
input_rules:
  - id: deny-pii-input
    severity: high
    match:
      topics: ["job.default"]
      scanners: ["pii"]
    decision: deny
    reason: "Input contains PII — redact before submitting"
  - id: require-approval-sensitive
    severity: high
    match:
      topics: ["job.default"]
      keywords: ["delete production", "drop table", "admin access"]
    decision: require_approval
    reason: "Task involves sensitive operations — human approval required"
`

// ---------- AutoGen templates ----------

const autogenAgentsPy = `"""AutoGen multi-agent system governed by Cordum.

This worker connects to Cordum via NATS, receives job requests, and executes
an AutoGen multi-agent conversation with governance. Cordum enforces rate
limiting between agents, content scanning, and escalation controls.
"""
import asyncio
import json
import os
from typing import Any

from cap.runtime import Agent, Context

# ---- AutoGen agent definitions ----
# Replace this with your real AutoGen agents. This example shows the
# integration pattern with Cordum governance.

try:
    from autogen import ConversableAgent, GroupChat, GroupChatManager

    planner = ConversableAgent(
        name="Planner",
        system_message="You break down complex tasks into actionable steps.",
        human_input_mode="NEVER",
    )

    executor = ConversableAgent(
        name="Executor",
        system_message="You execute the planned steps and report results.",
        human_input_mode="NEVER",
    )

    reviewer = ConversableAgent(
        name="Reviewer",
        system_message="You review the executor's work for correctness and completeness.",
        human_input_mode="NEVER",
    )

    AUTOGEN_AVAILABLE = True
except ImportError:
    AUTOGEN_AVAILABLE = False

# ---- Cordum worker ----

agent = Agent(
    sender_id=os.getenv("CORDUM_WORKER_ID", "autogen-worker"),
    pool=os.getenv("CORDUM_POOL", "default"),
)

@agent.job("job.default")
async def handle_multi_agent(ctx: Context, input: Any) -> dict:
    """Handle a multi-agent task dispatched by Cordum.

    Cordum's Safety Kernel enforces governance on every job: rate limiting
    prevents runaway agent conversations, content scanning blocks injection
    attacks between agents, and escalation controls prevent privilege creep.
    """
    task = ""
    if isinstance(input, dict):
        task = input.get("task", input.get("prompt", ""))
    elif isinstance(input, str):
        task = input

    if not task:
        return {"error": "No task provided. Send {\"task\": \"your task\"}"}

    ctx.logger.info("Processing multi-agent task", extra={"task": task[:100]})

    if AUTOGEN_AVAILABLE:
        group_chat = GroupChat(
            agents=[planner, executor, reviewer],
            messages=[],
            max_round=6,
        )
        manager = GroupChatManager(groupchat=group_chat)
        result = planner.initiate_chat(manager, message=task)
        return {
            "result": str(result.summary) if hasattr(result, "summary") else str(result),
            "agents_used": ["Planner", "Executor", "Reviewer"],
            "rounds": len(group_chat.messages),
        }

    # Fallback when autogen is not installed.
    return {
        "result": f"Processed: {task}",
        "note": "Install pyautogen for full multi-agent functionality",
    }

if __name__ == "__main__":
    asyncio.run(agent.run())
`

const autogenRequirements = `# Cordum Agent Protocol SDK
cap-sdk>=2.8.0

# AutoGen
pyautogen>=0.4.0

# NATS client
nats-py>=2.9.0

# Protobuf (required by CAP)
protobuf>=4.25.0
`

const autogenSafetyYAML = `# Safety policy for AutoGen multi-agent system
# Cordum governs every job dispatched to your agent group.
# Rate limiting, content scanning, and escalation controls are enforced
# at the platform level — your agents only see approved work.
default_decision: deny
output_policy:
  enabled: true
  fail_mode: closed
default_tenant: default
tenants:
  default:
    allow_topics:
      - "job.default"
    deny_topics:
      - "sys.*"
    allowed_repo_hosts: []
    denied_repo_hosts: []
    mcp:
      allow_servers: []
      deny_servers: []
      allow_tools: []
      deny_tools: []
      allow_resources: []
      deny_resources: []
      allow_actions: []
      deny_actions: []
rules:
  - id: allow-agent-tasks
    match:
      topics: ["job.default"]
    decision: allow
  - id: rate-limit-agents
    match:
      topics: ["job.default"]
    decision: allow
    velocity:
      max_requests: 10
      window_seconds: 60
input_rules:
  - id: deny-injection
    severity: high
    match:
      topics: ["job.default"]
      scanners: ["prompt_injection"]
    decision: deny
    reason: "Input contains prompt injection pattern"
`
