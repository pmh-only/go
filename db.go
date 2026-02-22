package main

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"log"
	"math/big"
	"regexp"
	"strings"
	"time"
)

var (
	db        *sql.DB
	validCode = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,32}$`)
)

// migrations is an ordered list of statement batches, one batch per schema version.
// Index 0 = migration to version 1, index 1 = migration to version 2, etc.
// Never edit existing entries — only append new ones.
var migrations = [][]string{
	// v1: initial schema
	{`CREATE TABLE IF NOT EXISTS urls (
		code             TEXT PRIMARY KEY,
		long_url         TEXT NOT NULL,
		public_enabled   INTEGER NOT NULL DEFAULT 1,
		internal_enabled INTEGER NOT NULL DEFAULT 1,
		created_at       TEXT NOT NULL
	)`},
	// v2: settings table for configurable hostnames
	{`CREATE TABLE IF NOT EXISTS settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`},
	// v3: redirect type and OpenGraph/Twitter meta fields
	{
		`ALTER TABLE urls ADD COLUMN redirect_type  TEXT NOT NULL DEFAULT 'redirect'`,
		`ALTER TABLE urls ADD COLUMN og_title       TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE urls ADD COLUMN og_description TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE urls ADD COLUMN og_image       TEXT NOT NULL DEFAULT ''`,
	},
	// v4: optional password protection for JS redirects
	{`ALTER TABLE urls ADD COLUMN password_hash TEXT NOT NULL DEFAULT ''`},
	// v5: user-facing description
	{`ALTER TABLE urls ADD COLUMN description TEXT NOT NULL DEFAULT ''`},
	// v6: optional expiry timestamp (RFC3339, empty = no expiry)
	{`ALTER TABLE urls ADD COLUMN expires_at TEXT NOT NULL DEFAULT ''`},
	// v7: use-count limiting (max_uses=0 means unlimited)
	{
		`ALTER TABLE urls ADD COLUMN max_uses  INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE urls ADD COLUMN use_count INTEGER NOT NULL DEFAULT 0`,
	},
}

func initDB() error {
	var err error
	db, err = sql.Open("sqlite", dbFile)
	if err != nil {
		return err
	}

	if _, err = db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return fmt.Errorf("set WAL mode: %w", err)
	}

	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}

	for i, stmts := range migrations[version:] {
		next := version + i + 1
		if err = applyMigration(next, stmts); err != nil {
			return fmt.Errorf("migration to v%d: %w", next, err)
		}
		log.Printf("db: migrated to schema v%d", next)
	}
	return nil
}

func applyMigration(targetVersion int, stmts []string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range stmts {
		if _, err = tx.Exec(stmt); err != nil {
			return err
		}
	}
	// PRAGMA user_version cannot be set via a parameterised query
	if _, err = tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", targetVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func generateCode() (string, error) {
	code := make([]byte, codeLen)
	for i := range code {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		code[i] = charset[n.Int64()]
	}
	return string(code), nil
}

type urlRecord struct {
	LongURL         string
	PublicEnabled   bool
	InternalEnabled bool
	RedirectType    string
	OGTitle         string
	OGDescription   string
	OGImage         string
	PasswordHash    string
	Description     string
	ExpiresAt       string
	MaxUses         int
	UseCount        int
}

// URLRow is used to render the URL list in the template.
type URLRow struct {
	Code            string
	LongURL         string
	PublicEnabled   bool
	InternalEnabled bool
	RedirectType    string
	OGTitle         string
	OGDescription   string
	OGImage         string
	HasPassword     bool
	Description     string
	CreatedAt       string
	ExpiresAt       string
	IsExpired       bool
	MaxUses         int
	UseCount        int
	UsesExhausted   bool
}

func saveURL(code, longURL string, publicEnabled, internalEnabled bool, redirectType, ogTitle, ogDescription, ogImage, passwordHash, description, expiresAt string, maxUses int) error {
	_, err := db.Exec(
		`INSERT INTO urls (code, long_url, public_enabled, internal_enabled, redirect_type, og_title, og_description, og_image, password_hash, description, expires_at, max_uses, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		code, longURL, boolToInt(publicEnabled), boolToInt(internalEnabled),
		redirectType, ogTitle, ogDescription, ogImage, passwordHash, description, expiresAt, maxUses,
		time.Now().UTC().Format("2006-01-02 15:04:05"),
	)
	return err
}

func getRecord(code string) (urlRecord, error) {
	var r urlRecord
	var pub, int_ int
	err := db.QueryRow(
		`SELECT long_url, public_enabled, internal_enabled, redirect_type, og_title, og_description, og_image, password_hash, description, expires_at, max_uses, use_count
		 FROM urls WHERE code = ?`, code,
	).Scan(&r.LongURL, &pub, &int_, &r.RedirectType, &r.OGTitle, &r.OGDescription, &r.OGImage, &r.PasswordHash, &r.Description, &r.ExpiresAt, &r.MaxUses, &r.UseCount)
	r.PublicEnabled = pub == 1
	r.InternalEnabled = int_ == 1
	return r, err
}

func getAllURLs() ([]URLRow, error) {
	rows, err := db.Query(
		`SELECT code, long_url, public_enabled, internal_enabled, redirect_type, og_title, og_description, og_image, password_hash, description, expires_at, max_uses, use_count, created_at
		 FROM urls ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var urls []URLRow
	for rows.Next() {
		var r URLRow
		var pub, int_ int
		var passwordHash string
		if err := rows.Scan(&r.Code, &r.LongURL, &pub, &int_, &r.RedirectType, &r.OGTitle, &r.OGDescription, &r.OGImage, &passwordHash, &r.Description, &r.ExpiresAt, &r.MaxUses, &r.UseCount, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.PublicEnabled = pub == 1
		r.InternalEnabled = int_ == 1
		r.HasPassword = passwordHash != ""
		if r.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, r.ExpiresAt); err == nil {
				r.IsExpired = time.Now().UTC().After(t)
			}
		}
		r.UsesExhausted = r.MaxUses > 0 && r.UseCount >= r.MaxUses
		urls = append(urls, r)
	}
	return urls, rows.Err()
}

func updateURL(code string, longURL *string, publicEnabled, internalEnabled *bool, redirectType, ogTitle, ogDescription, ogImage, passwordHash, description, expiresAt *string, maxUses *int) error {
	var sets []string
	var args []any

	if longURL != nil {
		sets = append(sets, "long_url = ?")
		args = append(args, *longURL)
	}
	if publicEnabled != nil {
		sets = append(sets, "public_enabled = ?")
		args = append(args, boolToInt(*publicEnabled))
	}
	if internalEnabled != nil {
		sets = append(sets, "internal_enabled = ?")
		args = append(args, boolToInt(*internalEnabled))
	}
	if redirectType != nil {
		sets = append(sets, "redirect_type = ?")
		args = append(args, *redirectType)
	}
	if ogTitle != nil {
		sets = append(sets, "og_title = ?")
		args = append(args, *ogTitle)
	}
	if ogDescription != nil {
		sets = append(sets, "og_description = ?")
		args = append(args, *ogDescription)
	}
	if ogImage != nil {
		sets = append(sets, "og_image = ?")
		args = append(args, *ogImage)
	}
	if passwordHash != nil {
		sets = append(sets, "password_hash = ?")
		args = append(args, *passwordHash)
	}
	if description != nil {
		sets = append(sets, "description = ?")
		args = append(args, *description)
	}
	if expiresAt != nil {
		sets = append(sets, "expires_at = ?")
		args = append(args, *expiresAt)
	}
	if maxUses != nil {
		sets = append(sets, "max_uses = ?")
		args = append(args, *maxUses)
	}
	if len(sets) == 0 {
		return nil
	}

	args = append(args, code)
	_, err := db.Exec("UPDATE urls SET "+strings.Join(sets, ", ")+" WHERE code = ?", args...)
	return err
}

// incrementUseCount atomically increments use_count.
// When maxUses > 0 it only increments while use_count < max_uses and returns
// withinLimit=false (without incrementing) once the limit is reached.
func incrementUseCount(code string, maxUses int) (withinLimit bool, err error) {
	var res sql.Result
	if maxUses == 0 {
		res, err = db.Exec("UPDATE urls SET use_count = use_count + 1 WHERE code = ?", code)
	} else {
		res, err = db.Exec("UPDATE urls SET use_count = use_count + 1 WHERE code = ? AND use_count < max_uses", code)
	}
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func deleteURL(code string) error {
	res, err := db.Exec("DELETE FROM urls WHERE code = ?", code)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
