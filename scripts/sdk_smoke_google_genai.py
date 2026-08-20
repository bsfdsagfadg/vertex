"""Black-box smoke test using the official google-genai Python SDK.

Required: V2_API_KEY and V2_MODEL.
Optional: V2_GEMINI_BASE_URL (default http://127.0.0.1:2156).
Pin google-genai to the version recorded by the release environment.
"""

import os

from google import genai
from google.genai import types


def require(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        raise SystemExit(f"missing required environment variable: {name}")
    return value


def main() -> None:
    model = require("V2_MODEL")
    client = genai.Client(
        api_key=require("V2_API_KEY"),
        http_options=types.HttpOptions(
            base_url=os.environ.get("V2_GEMINI_BASE_URL", "http://127.0.0.1:2156").rstrip("/"),
            api_version="v1beta",
            timeout=60_000,
        ),
    )

    first_model = next(iter(client.models.list(config={"page_size": 1})), None)
    assert first_model is not None, "models.list returned no models"

    response = client.models.generate_content(
        model=model,
        contents="Reply with exactly: GEMINI-OK",
        config=types.GenerateContentConfig(temperature=0),
    )
    assert response.text, "generate_content returned no text"

    chunks = list(
        client.models.generate_content_stream(
            model=model,
            contents="Reply with exactly: GEMINI-STREAM-OK",
            config=types.GenerateContentConfig(temperature=0),
        )
    )
    assert any(chunk.text for chunk in chunks), "generate_content_stream returned no text"
    print("Google Gen AI SDK smoke passed", {"model": model})


if __name__ == "__main__":
    main()
