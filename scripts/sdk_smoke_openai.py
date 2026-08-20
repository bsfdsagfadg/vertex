"""Black-box smoke test using the official OpenAI Python SDK.

Required: V2_API_KEY and V2_MODEL.
Optional: V2_OPENAI_BASE_URL (default http://127.0.0.1:2156/v1).
"""

import os

from openai import OpenAI


def require(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        raise SystemExit(f"missing required environment variable: {name}")
    return value


def main() -> None:
    model = require("V2_MODEL")
    client = OpenAI(
        api_key=require("V2_API_KEY"),
        base_url=os.environ.get("V2_OPENAI_BASE_URL", "http://127.0.0.1:2156/v1").rstrip("/"),
        timeout=60.0,
        max_retries=0,
    )

    models = client.models.list()
    assert models.data, "models.list returned no models"

    chat = client.chat.completions.create(
        model=model,
        messages=[{"role": "user", "content": "Reply with exactly: V2-OK"}],
        temperature=0,
    )
    assert chat.choices and chat.choices[0].message, "chat.completions returned no message"

    streamed = []
    for chunk in client.chat.completions.create(
        model=model,
        messages=[{"role": "user", "content": "Reply with exactly: STREAM-OK"}],
        temperature=0,
        stream=True,
    ):
        if chunk.choices and chunk.choices[0].delta.content:
            streamed.append(chunk.choices[0].delta.content)
    assert streamed, "chat.completions stream returned no text delta"

    response = client.responses.create(model=model, input="Reply with exactly: RESPONSES-OK")
    assert response.id and response.status == "completed", "responses.create did not complete"

    completion = client.completions.create(model=model, prompt="Reply with exactly: LEGACY-OK", max_tokens=32)
    assert completion.choices, "legacy completions returned no choice"
    print("OpenAI SDK smoke passed", {"chat": chat.id, "response": response.id, "completion": completion.id})


if __name__ == "__main__":
    main()
