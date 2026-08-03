package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPURLAndSettingsManagement(t *testing.T) {
	oldDB, oldDBFile := db, dbFile
	oldSettings := currentSettings()
	dbFile = filepath.Join(t.TempDir(), "urls.db")
	if err := initDB(); err != nil {
		t.Fatal(err)
	}
	if err := loadSettings(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		db = oldDB
		dbFile = oldDBFile
		cfg.apply(oldSettings.PublicBase, oldSettings.UIHost, oldSettings.InternalHost, oldSettings.AliasHost, oldSettings.PublicAPIHost)
	})

	mux := http.NewServeMux()
	mux.Handle("/mcp", newMCPHandler())
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	originRequest, err := http.NewRequest(http.MethodPost, httpServer.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	originRequest.Header.Set("Origin", "https://attacker.example")
	originResponse, err := http.DefaultClient.Do(originRequest)
	if err != nil {
		t.Fatal(err)
	}
	originResponse.Body.Close()
	if originResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("Origin request returned %d, want %d", originResponse.StatusCode, http.StatusForbidden)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             httpServer.URL + "/mcp",
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	callTool := func(name string, arguments map[string]any) {
		t.Helper()
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if result.IsError {
			t.Fatalf("%s returned tool error: %v", name, result.Content)
		}
	}

	callTool("create_url", map[string]any{
		"url":         "https://example.com/original",
		"custom_code": "agent-test",
		"description": "created through MCP",
	})
	created, err := getURLRow("agent-test")
	if err != nil {
		t.Fatal(err)
	}
	if created.LongURL != "https://example.com/original" {
		t.Fatalf("unexpected destination: %q", created.LongURL)
	}

	callTool("list_urls", map[string]any{})
	callTool("get_url", map[string]any{"code": "agent-test"})
	callTool("update_url", map[string]any{
		"code":        "agent-test",
		"new_code":    "agent-renamed",
		"long_url":    "https://example.com/updated",
		"description": "updated through MCP",
	})
	updated, err := getURLRow("agent-renamed")
	if err != nil {
		t.Fatal(err)
	}
	if updated.LongURL != "https://example.com/updated" || updated.Description != "updated through MCP" {
		t.Fatalf("URL was not updated: %+v", updated)
	}

	callTool("get_settings", map[string]any{})
	callTool("update_settings", map[string]any{"public_base": "https://short.example"})
	if settings := currentSettings(); settings.PublicBase != "https://short.example" {
		t.Fatalf("settings were not updated: %+v", settings)
	}

	callTool("delete_url", map[string]any{"code": "agent-renamed"})
	if _, err := getURLRow("agent-renamed"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("URL was not deleted: %v", err)
	}
}
