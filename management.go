package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

var managementMu sync.Mutex

type requestError struct {
	message string
}

func (e *requestError) Error() string { return e.message }

type conflictError struct {
	message string
}

func (e *conflictError) Error() string { return e.message }

type createURLInput struct {
	URL             string `json:"url" jsonschema:"the destination URL"`
	CustomCode      string `json:"custom_code,omitempty" jsonschema:"optional custom short code; 1-32 letters, numbers, hyphens, or underscores"`
	PublicEnabled   *bool  `json:"public_enabled,omitempty" jsonschema:"whether the public URL is enabled; defaults to true"`
	InternalEnabled *bool  `json:"internal_enabled,omitempty" jsonschema:"whether the internal URL is enabled; defaults to true"`
	RedirectType    string `json:"redirect_type,omitempty" jsonschema:"redirect mode: redirect, meta, or js"`
	OGTitle         string `json:"og_title,omitempty" jsonschema:"OpenGraph title for meta and JavaScript redirects"`
	OGDescription   string `json:"og_description,omitempty" jsonschema:"OpenGraph description for meta and JavaScript redirects"`
	OGImage         string `json:"og_image,omitempty" jsonschema:"OpenGraph image URL for meta and JavaScript redirects"`
	Password        string `json:"password,omitempty" jsonschema:"optional password for JavaScript redirects"`
	Description     string `json:"description,omitempty" jsonschema:"human-readable note about the short URL"`
	ExpiresAt       string `json:"expires_at,omitempty" jsonschema:"optional RFC3339 expiry timestamp"`
	MaxUses         int    `json:"max_uses,omitempty" jsonschema:"maximum successful redirects; zero means unlimited"`
}

type updateURLInput struct {
	Code            string  `json:"-"`
	NewCode         *string `json:"code,omitempty"`
	LongURL         *string `json:"long_url,omitempty"`
	PublicEnabled   *bool   `json:"public_enabled,omitempty"`
	InternalEnabled *bool   `json:"internal_enabled,omitempty"`
	RedirectType    *string `json:"redirect_type,omitempty"`
	OGTitle         *string `json:"og_title,omitempty"`
	OGDescription   *string `json:"og_description,omitempty"`
	OGImage         *string `json:"og_image,omitempty"`
	Password        *string `json:"password,omitempty"`
	Description     *string `json:"description,omitempty"`
	ExpiresAt       *string `json:"expires_at,omitempty"`
	MaxUses         *int    `json:"max_uses,omitempty"`
}

func normalizeRedirectType(value string) string {
	if value == "meta" || value == "js" {
		return value
	}
	return "redirect"
}

func validateExpiresAt(value string) error {
	if value == "" {
		return nil
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return &requestError{message: "expires_at must be RFC3339 (e.g. 2026-03-01T00:00:00Z)"}
	}
	return nil
}

func createManagedURL(input createURLInput) (URLRow, error) {
	managementMu.Lock()
	defer managementMu.Unlock()

	longURL := strings.TrimSpace(input.URL)
	if longURL == "" {
		return URLRow{}, &requestError{message: "url is required"}
	}

	publicEnabled := input.PublicEnabled == nil || *input.PublicEnabled
	internalEnabled := input.InternalEnabled == nil || *input.InternalEnabled
	if !publicEnabled && !internalEnabled {
		return URLRow{}, &requestError{message: "at least one link type (public_enabled or internal_enabled) must be true"}
	}
	if err := validateExpiresAt(input.ExpiresAt); err != nil {
		return URLRow{}, err
	}

	redirectType := normalizeRedirectType(input.RedirectType)
	passwordHash := ""
	if input.Password != "" {
		passwordHash = hashPassword(input.Password)
	}
	maxUses := input.MaxUses
	if maxUses < 0 {
		maxUses = 0
	}

	customCode := strings.TrimSpace(input.CustomCode)
	if customCode != "" {
		if !validCode.MatchString(customCode) {
			return URLRow{}, &requestError{message: "custom alias must be 1-32 chars: letters, numbers, hyphens, underscores"}
		}
		if err := saveURL(customCode, longURL, publicEnabled, internalEnabled, redirectType, input.OGTitle, input.OGDescription, input.OGImage, passwordHash, input.Description, input.ExpiresAt, maxUses); err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				return URLRow{}, &conflictError{message: fmt.Sprintf("alias '%s' is already taken", customCode)}
			}
			return URLRow{}, err
		}
		return getURLRow(customCode)
	}

	for {
		code, err := generateCode()
		if err != nil {
			return URLRow{}, err
		}
		err = saveURL(code, longURL, publicEnabled, internalEnabled, redirectType, input.OGTitle, input.OGDescription, input.OGImage, passwordHash, input.Description, input.ExpiresAt, maxUses)
		if err == nil {
			return getURLRow(code)
		}
		if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return URLRow{}, err
		}
	}
}

func updateManagedURL(input updateURLInput) (URLRow, error) {
	managementMu.Lock()
	defer managementMu.Unlock()

	rec, err := getRecord(input.Code)
	if err != nil {
		return URLRow{}, err
	}

	if input.LongURL != nil && strings.TrimSpace(*input.LongURL) == "" {
		return URLRow{}, &requestError{message: "long_url cannot be empty"}
	}
	if input.RedirectType != nil {
		redirectType := normalizeRedirectType(*input.RedirectType)
		input.RedirectType = &redirectType
	}
	if input.ExpiresAt != nil {
		if err := validateExpiresAt(*input.ExpiresAt); err != nil {
			return URLRow{}, err
		}
	}
	if input.MaxUses != nil && *input.MaxUses < 0 {
		maxUses := 0
		input.MaxUses = &maxUses
	}

	var passwordHash *string
	if input.Password != nil {
		hash := ""
		if *input.Password != "" {
			hash = hashPassword(*input.Password)
		}
		passwordHash = &hash
	}

	if input.NewCode == nil {
		if err := updateURL(input.Code, input.LongURL, input.PublicEnabled, input.InternalEnabled, input.RedirectType, input.OGTitle, input.OGDescription, input.OGImage, passwordHash, input.Description, input.ExpiresAt, input.MaxUses); err != nil {
			return URLRow{}, err
		}
		return getURLRow(input.Code)
	}

	newCode := strings.TrimSpace(*input.NewCode)
	if !validCode.MatchString(newCode) {
		return URLRow{}, &requestError{message: "code must be 1-32 chars: letters, numbers, hyphens, underscores"}
	}

	longURL := rec.LongURL
	if input.LongURL != nil {
		longURL = *input.LongURL
	}
	publicEnabled := rec.PublicEnabled
	if input.PublicEnabled != nil {
		publicEnabled = *input.PublicEnabled
	}
	internalEnabled := rec.InternalEnabled
	if input.InternalEnabled != nil {
		internalEnabled = *input.InternalEnabled
	}
	redirectType := rec.RedirectType
	if input.RedirectType != nil {
		redirectType = *input.RedirectType
	}
	ogTitle := rec.OGTitle
	if input.OGTitle != nil {
		ogTitle = *input.OGTitle
	}
	ogDescription := rec.OGDescription
	if input.OGDescription != nil {
		ogDescription = *input.OGDescription
	}
	ogImage := rec.OGImage
	if input.OGImage != nil {
		ogImage = *input.OGImage
	}
	oldPasswordHash := rec.PasswordHash
	if passwordHash != nil {
		oldPasswordHash = *passwordHash
	}
	description := rec.Description
	if input.Description != nil {
		description = *input.Description
	}
	expiresAt := rec.ExpiresAt
	if input.ExpiresAt != nil {
		expiresAt = *input.ExpiresAt
	}
	maxUses := rec.MaxUses
	if input.MaxUses != nil {
		maxUses = *input.MaxUses
	}

	tx, err := db.Begin()
	if err != nil {
		return URLRow{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		"INSERT INTO urls (code, long_url, public_enabled, internal_enabled, redirect_type, og_title, og_description, og_image, password_hash, description, expires_at, max_uses, use_count, created_at) SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, created_at FROM urls WHERE code = ?",
		newCode, longURL, boolToInt(publicEnabled), boolToInt(internalEnabled), redirectType, ogTitle, ogDescription, ogImage, oldPasswordHash, description, expiresAt, maxUses, input.Code,
	); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return URLRow{}, &conflictError{message: fmt.Sprintf("code '%s' is already taken", newCode)}
		}
		return URLRow{}, err
	}
	if _, err := tx.Exec("DELETE FROM urls WHERE code = ?", input.Code); err != nil {
		return URLRow{}, err
	}
	if err := tx.Commit(); err != nil {
		return URLRow{}, err
	}
	return getURLRow(newCode)
}

func deleteManagedURL(code string) error {
	managementMu.Lock()
	defer managementMu.Unlock()
	return deleteURL(code)
}
