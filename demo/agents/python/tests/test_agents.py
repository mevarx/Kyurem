import asyncio
import json
import unittest

import httpx

from kyurem_demo.agents import create_app


class AgentServerTests(unittest.TestCase):
    def call(self, app, request_id):
        async def run():
            transport = httpx.ASGITransport(app=app)
            async with httpx.AsyncClient(transport=transport, base_url="http://test") as client:
                return await client.post("/", headers={"A2A-Version": "1.0"}, json={
                    "jsonrpc": "2.0",
                    "id": request_id,
                    "method": "SendMessage",
                    "params": {"message": {
                        "messageId": f"{request_id}-message",
                        "role": "ROLE_USER",
                        "parts": [{"text": "hello Kyurem"}],
                    }},
                })
        return asyncio.run(run())

    def test_echo_returns_a_completed_a2a_task(self):
        response = self.call(create_app("echo", 9012), "echo")
        self.assertEqual(response.status_code, 200, response.text)
        payload = json.dumps(response.json())
        self.assertIn("python echo: hello Kyurem", payload)
        self.assertIn("TASK_STATE_COMPLETED", payload)

    def test_flaky_agent_fails_before_processing_then_recovers(self):
        app = create_app("flaky", 9014, failures=1)
        first = self.call(app, "first")
        second = self.call(app, "second")
        self.assertEqual(first.status_code, 503, first.text)
        self.assertEqual(second.status_code, 200, second.text)
        self.assertIn("python flaky recovered: hello Kyurem", json.dumps(second.json()))

    def test_tck_message_response_returns_message_not_task(self):
        request_id = "tck-message-response-test"
        response = self.call(create_app("echo", 9012), request_id)
        result = response.json()["result"]
        self.assertEqual(result["message"]["role"], "ROLE_AGENT")
        self.assertNotIn("task", result)
        self.assertIn("Direct message response", json.dumps(result))

    def test_tck_artifact_behavior_returns_expected_text(self):
        response = self.call(create_app("echo", 9012), "tck-artifact-text-test")
        artifact = response.json()["result"]["task"]["artifacts"][0]
        self.assertEqual(artifact["parts"][0]["text"], "Generated text content")


if __name__ == "__main__":
    unittest.main()
