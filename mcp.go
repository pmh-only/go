package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"mime"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpURL struct {
	Code            string `json:"code"`
	LongURL         string `json:"long_url"`
	PublicEnabled   bool   `json:"public_enabled"`
	InternalEnabled bool   `json:"internal_enabled"`
	RedirectType    string `json:"redirect_type"`
	OGTitle         string `json:"og_title"`
	OGDescription   string `json:"og_description"`
	OGImage         string `json:"og_image"`
	HasPassword     bool   `json:"has_password"`
	Description     string `json:"description"`
	CreatedAt       string `json:"created_at"`
	ExpiresAt       string `json:"expires_at"`
	IsExpired       bool   `json:"is_expired"`
	MaxUses         int    `json:"max_uses"`
	UseCount        int    `json:"use_count"`
	UsesExhausted   bool   `json:"uses_exhausted"`
	ShortURL        string `json:"short_url"`
	AliasURL        string `json:"alias_url"`
	InternalURL     string `json:"internal_url"`
}

type mcpURLResult struct {
	URL mcpURL `json:"url"`
}

type mcpURLListResult struct {
	URLs []mcpURL `json:"urls"`
}

type mcpCodeInput struct {
	Code string `json:"code" jsonschema:"short code to look up or delete"`
}

type mcpUpdateURLInput struct {
	Code            string  `json:"code" jsonschema:"current short code"`
	NewCode         *string `json:"new_code,omitempty" jsonschema:"replacement short code; 1-32 letters, numbers, hyphens, or underscores"`
	LongURL         *string `json:"long_url,omitempty" jsonschema:"replacement destination URL"`
	PublicEnabled   *bool   `json:"public_enabled,omitempty" jsonschema:"whether the public URL is enabled"`
	InternalEnabled *bool   `json:"internal_enabled,omitempty" jsonschema:"whether the internal URL is enabled"`
	RedirectType    *string `json:"redirect_type,omitempty" jsonschema:"redirect mode: redirect, meta, or js"`
	OGTitle         *string `json:"og_title,omitempty" jsonschema:"OpenGraph title"`
	OGDescription   *string `json:"og_description,omitempty" jsonschema:"OpenGraph description"`
	OGImage         *string `json:"og_image,omitempty" jsonschema:"OpenGraph image URL"`
	Password        *string `json:"password,omitempty" jsonschema:"replacement password; empty removes password protection"`
	Description     *string `json:"description,omitempty" jsonschema:"replacement human-readable note"`
	ExpiresAt       *string `json:"expires_at,omitempty" jsonschema:"replacement RFC3339 expiry timestamp; empty removes expiry"`
	MaxUses         *int    `json:"max_uses,omitempty" jsonschema:"replacement maximum redirects; zero means unlimited"`
}

type mcpDeleteResult struct {
	Code    string `json:"code"`
	Deleted bool   `json:"deleted"`
}

type mcpSettingsResult struct {
	Settings settingsValues `json:"settings"`
}

type mcpURLBases struct {
	publicBase  string
	internalURL string
	aliasBase   string
}

func boolPtr(value bool) *bool { return &value }

func readOnlyAnnotations() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		ReadOnlyHint:  true,
		OpenWorldHint: boolPtr(false),
	}
}

func writeAnnotations(destructive, idempotent bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		DestructiveHint: boolPtr(destructive),
		IdempotentHint:  idempotent,
		OpenWorldHint:   boolPtr(false),
	}
}

func mcpURLFromRow(row URLRow) mcpURL {
	return mcpURLFromRowWithBases(row, currentMCPURLBases())
}

func currentMCPURLBases() mcpURLBases {
	publicBase, _, _, internalHost, aliasHost, _ := cfg.fullSnapshot()
	return mcpURLBases{
		publicBase:  publicBase,
		internalURL: hostOf(internalHost),
		aliasBase:   aliasBaseFrom(aliasHost, publicBase),
	}
}

func mcpURLFromRowWithBases(row URLRow, bases mcpURLBases) mcpURL {
	result := mcpURL{
		Code:            row.Code,
		LongURL:         row.LongURL,
		PublicEnabled:   row.PublicEnabled,
		InternalEnabled: row.InternalEnabled,
		RedirectType:    row.RedirectType,
		OGTitle:         row.OGTitle,
		OGDescription:   row.OGDescription,
		OGImage:         row.OGImage,
		HasPassword:     row.HasPassword,
		Description:     row.Description,
		CreatedAt:       row.CreatedAt,
		ExpiresAt:       row.ExpiresAt,
		IsExpired:       row.IsExpired,
		MaxUses:         row.MaxUses,
		UseCount:        row.UseCount,
		UsesExhausted:   row.UsesExhausted,
	}
	if row.PublicEnabled {
		result.ShortURL = fmt.Sprintf("%s/%s", bases.publicBase, row.Code)
		if bases.aliasBase != "" {
			result.AliasURL = fmt.Sprintf("%s/%s", bases.aliasBase, row.Code)
		}
	}
	if row.InternalEnabled {
		result.InternalURL = fmt.Sprintf("%s/%s", bases.internalURL, row.Code)
	}
	return result
}

func mcpManagementError(action string, err error) error {
	var invalid *requestError
	var conflict *conflictError
	switch {
	case errors.As(err, &invalid):
		return errors.New(invalid.Error())
	case errors.As(err, &conflict):
		return errors.New(conflict.Error())
	case errors.Is(err, sql.ErrNoRows):
		return errors.New("URL not found")
	default:
		return fmt.Errorf("%s: %w", action, err)
	}
}

func newMCPHandler() http.Handler {
	version := buildVersion
	if version == "" {
		version = "dev"
	}
	server := mcp.NewServer(
		&mcp.Implementation{Name: "gourl", Version: version},
		&mcp.ServerOptions{Instructions: "Manage the URL shortener. All changes are applied immediately and the server does not require authentication."},
	)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_urls",
		Description: "List every short URL and its complete configuration and usage state.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, mcpURLListResult, error) {
		rows, err := getAllURLsContext(ctx)
		if err != nil {
			return nil, mcpURLListResult{}, mcpManagementError("list URLs", err)
		}
		urls := make([]mcpURL, 0, len(rows))
		bases := currentMCPURLBases()
		for _, row := range rows {
			urls = append(urls, mcpURLFromRowWithBases(row, bases))
		}
		return nil, mcpURLListResult{URLs: urls}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_url",
		Description: "Get one short URL by code, including its configuration and usage state.",
		Annotations: readOnlyAnnotations(),
	}, func(_ context.Context, _ *mcp.CallToolRequest, input mcpCodeInput) (*mcp.CallToolResult, mcpURLResult, error) {
		row, err := getURLRow(input.Code)
		if err != nil {
			return nil, mcpURLResult{}, mcpManagementError("get URL", err)
		}
		return nil, mcpURLResult{URL: mcpURLFromRow(row)}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_url",
		Description: "Create a short URL with an optional custom code and full redirect, metadata, password, expiry, and usage-limit configuration.",
		Annotations: writeAnnotations(false, false),
	}, func(_ context.Context, _ *mcp.CallToolRequest, input createURLInput) (*mcp.CallToolResult, mcpURLResult, error) {
		row, err := createManagedURL(input)
		if err != nil {
			return nil, mcpURLResult{}, mcpManagementError("create URL", err)
		}
		return nil, mcpURLResult{URL: mcpURLFromRow(row)}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "update_url",
		Description: "Update any mutable field of a short URL. Omitted fields are unchanged; an empty password or expiry removes it.",
		Annotations: writeAnnotations(true, true),
	}, func(_ context.Context, _ *mcp.CallToolRequest, input mcpUpdateURLInput) (*mcp.CallToolResult, mcpURLResult, error) {
		row, err := updateManagedURL(updateURLInput{
			Code:            input.Code,
			NewCode:         input.NewCode,
			LongURL:         input.LongURL,
			PublicEnabled:   input.PublicEnabled,
			InternalEnabled: input.InternalEnabled,
			RedirectType:    input.RedirectType,
			OGTitle:         input.OGTitle,
			OGDescription:   input.OGDescription,
			OGImage:         input.OGImage,
			Password:        input.Password,
			Description:     input.Description,
			ExpiresAt:       input.ExpiresAt,
			MaxUses:         input.MaxUses,
		})
		if err != nil {
			return nil, mcpURLResult{}, mcpManagementError("update URL", err)
		}
		return nil, mcpURLResult{URL: mcpURLFromRow(row)}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_url",
		Description: "Permanently delete a short URL by code.",
		Annotations: writeAnnotations(true, false),
	}, func(_ context.Context, _ *mcp.CallToolRequest, input mcpCodeInput) (*mcp.CallToolResult, mcpDeleteResult, error) {
		if err := deleteManagedURL(input.Code); err != nil {
			return nil, mcpDeleteResult{}, mcpManagementError("delete URL", err)
		}
		return nil, mcpDeleteResult{Code: input.Code, Deleted: true}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_settings",
		Description: "Get all runtime hostname settings.",
		Annotations: readOnlyAnnotations(),
	}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, mcpSettingsResult, error) {
		return nil, mcpSettingsResult{Settings: currentSettings()}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "update_settings",
		Description: "Update runtime hostname settings. Omitted settings are unchanged; changes are persisted and take effect immediately.",
		Annotations: writeAnnotations(true, true),
	}, func(_ context.Context, _ *mcp.CallToolRequest, input settingsUpdate) (*mcp.CallToolResult, mcpSettingsResult, error) {
		settings, err := updateSettings(input)
		if err != nil {
			return nil, mcpSettingsResult{}, fmt.Errorf("update settings: %w", err)
		}
		return nil, mcpSettingsResult{Settings: settings}, nil
	})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		JSONResponse: true,
		Stateless:    true,
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// MCP has no browser client, so reject Origin-bearing requests to prevent
		// cross-site and DNS-rebinding attacks without adding authentication.
		if r.Header.Get("Origin") != "" {
			http.Error(w, "browser-origin requests are not allowed", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost {
			mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mediaType != "application/json" {
				http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		}
		handler.ServeHTTP(w, r)
	})
}
