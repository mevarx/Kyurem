from __future__ import annotations

import argparse
import asyncio
from collections.abc import Callable
from typing import Any

import uvicorn
from a2a.helpers import get_message_text, new_task_from_user_message, new_text_message, new_text_part
from a2a.server.agent_execution import AgentExecutor, RequestContext
from a2a.server.events import EventQueue
from a2a.server.request_handlers import DefaultRequestHandler
from a2a.server.routes import create_agent_card_routes, create_jsonrpc_routes
from a2a.server.tasks import InMemoryTaskStore, TaskUpdater
from a2a.types import AgentCapabilities, AgentCard, AgentInterface, AgentSkill, Part, TaskState
from google.protobuf.json_format import ParseDict
from starlette.applications import Starlette


class TextAgentExecutor(AgentExecutor):
    def __init__(self, transform: Callable[[str], str]) -> None:
        self.transform = transform

    async def execute(self, context: RequestContext, event_queue: EventQueue) -> None:
        message_id = context.message.message_id if context.message else ""
        text = get_message_text(context.message).strip()
        if "message-response" in message_id or "tck-message-response" in text:
            await event_queue.enqueue_event(new_text_message("Direct message response"))
            return
        task = context.current_task or new_task_from_user_message(context.message)
        if context.current_task is None:
            await event_queue.enqueue_event(task)
        updater = TaskUpdater(event_queue, task.id, task.context_id)
        await updater.update_status(
            state=TaskState.TASK_STATE_WORKING,
            message=new_text_message("Processing request"),
        )
        reply = self.transform(text)
        part = new_text_part(text=reply, media_type="text/plain")
        if "artifact-text" in message_id:
            part = new_text_part(text="Generated text content", media_type="text/plain")
        elif "artifact-file-url" in message_id:
            part = Part(url="https://example.com/output.txt", filename="output.txt", media_type="text/plain")
        elif "artifact-file" in message_id:
            part = Part(raw=b"Generated file content", filename="output.txt", media_type="text/plain")
        elif "artifact-data" in message_id:
            data = ParseDict({"key": "value", "count": 42}, Part().data)
            part = Part(data=data)
        await updater.add_artifact(parts=[part])
        await updater.update_status(
            state=TaskState.TASK_STATE_COMPLETED,
            message=new_text_message("Request completed"),
        )

    async def cancel(self, context: RequestContext, event_queue: EventQueue) -> None:
        del context, event_queue


class FailFirstRequest:
    """Return a deterministic pre-processing 503 before serving requests."""

    def __init__(self, app: Any, failures: int) -> None:
        self.app = app
        self.remaining = failures
        self.lock = asyncio.Lock()

    async def __call__(self, scope: dict[str, Any], receive: Any, send: Any) -> None:
        should_fail = False
        if scope["type"] == "http" and scope["method"] == "POST":
            async with self.lock:
                if self.remaining > 0:
                    self.remaining -= 1
                    should_fail = True
        if should_fail:
            body = b'{"error":"demo_flaky_upstream","message":"planned transient failure"}'
            await send({"type": "http.response.start", "status": 503, "headers": [(b"content-type", b"application/json"), (b"content-length", str(len(body)).encode())]})
            await send({"type": "http.response.body", "body": body})
            return
        await self.app(scope, receive, send)


def create_app(agent: str, port: int, failures: int = 1) -> Any:
    transforms = {
        "echo": lambda text: f"python echo: {text}",
        "upper": lambda text: f"python upper: {text.upper()}",
        "flaky": lambda text: f"python flaky recovered: {text}",
    }
    if agent not in transforms:
        raise ValueError(f"unknown agent: {agent}")
    skill = AgentSkill(
        id=f"python-{agent}",
        name=f"Python {agent.title()}",
        description=f"A small {agent} demo agent implemented with a2a-python.",
        tags=["demo", "python"],
        examples=["hello from Kyurem"],
    )
    card = AgentCard(
        name=f"Kyurem Python {agent.title()} Agent",
        description=f"A minimal A2A {agent} agent used in Kyurem demos.",
        version="0.1.0",
        default_input_modes=["text/plain"],
        default_output_modes=["text/plain"],
        capabilities=AgentCapabilities(streaming=True),
        supported_interfaces=[AgentInterface(
            protocol_binding="JSONRPC",
            url=f"http://127.0.0.1:{port}",
            protocol_version="1.0",
        )],
        skills=[skill],
    )
    handler = DefaultRequestHandler(
        agent_executor=TextAgentExecutor(transforms[agent]),
        task_store=InMemoryTaskStore(),
        agent_card=card,
    )
    routes = [*create_agent_card_routes(card), *create_jsonrpc_routes(handler, "/")]
    app = Starlette(routes=routes)
    if agent == "flaky":
        return FailFirstRequest(app, failures)
    return app


def serve(agent: str, argv: list[str] | None = None) -> None:
    parser = argparse.ArgumentParser(description=f"Run the Python {agent} A2A demo agent")
    parser.add_argument("--host", default="0.0.0.0")
    parser.add_argument("--port", type=int, default=9012)
    parser.add_argument("--failures", type=int, default=1, help="planned 503 responses for the flaky agent")
    args = parser.parse_args(argv)
    uvicorn.run(create_app(agent, args.port, args.failures), host=args.host, port=args.port)
