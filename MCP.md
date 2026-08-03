# MCP interface

The application exposes an unauthenticated Streamable HTTP MCP endpoint on a
separate listener:

```text
http://HOST:8081/mcp
```

Set `MCP_PORT` to change the listen address or port. It defaults to `:8081`,
which listens on every network interface. The listener intentionally has no
authentication and its tools can change or delete all application data, so
restrict access at the network or container boundary when needed.

Available tools:

| Tool | Operation |
| --- | --- |
| `list_urls` | List all short URLs and usage state |
| `get_url` | Read one short URL by code |
| `create_url` | Create a fully configured short URL |
| `update_url` | Update or rename a short URL |
| `delete_url` | Permanently delete a short URL |
| `get_settings` | Read hostname settings |
| `update_settings` | Persist and immediately apply hostname settings |

Example client configuration:

```json
{
  "mcpServers": {
    "gourl": {
      "type": "remote",
      "url": "http://localhost:8081/mcp"
    }
  }
}
```
