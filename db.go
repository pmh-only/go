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
	CreatedAt       string
}

func saveURL(code, longURL string, publicEnabled, internalEnabled bool, redirectType, ogTitle, ogDescription, ogImage string) error {
	_, err := db.Exec(
		`INSERT INTO urls (code, long_url, public_enabled, internal_enabled, redirect_type, og_title, og_description, og_image, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		code, longURL, boolToInt(publicEnabled), boolToInt(internalEnabled),
		redirectType, ogTitle, ogDescription, ogImage,
		time.Now().UTC().Format("2006-01-02 15:04:05"),
	)
	return err
}

func getRecord(code string) (urlRecord, error) {
	var r urlRecord
	var pub, int_ int
	err := db.QueryRow(
		`SELECT long_url, public_enabled, internal_enabled, redirect_type, og_title, og_description, og_image
		 FROM urls WHERE code = ?`, code,
	).Scan(&r.LongURL, &pub, &int_, &r.RedirectType, &r.OGTitle, &r.OGDescription, &r.OGImage)
	r.PublicEnabled = pub == 1
	r.InternalEnabled = int_ == 1
	return r, err
}

func getAllURLs() ([]URLRow, error) {
	rows, err := db.Query(
		`SELECT code, long_url, public_enabled, internal_enabled, redirect_type, og_title, og_description, og_image, created_at
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
		if err := rows.Scan(&r.Code, &r.LongURL, &pub, &int_, &r.RedirectType, &r.OGTitle, &r.OGDescription, &r.OGImage, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.PublicEnabled = pub == 1
		r.InternalEnabled = int_ == 1
		urls = append(urls, r)
	}
	return urls, rows.Err()
}

func updateURL(code string, longURL *string, publicEnabled, internalEnabled *bool, redirectType, ogTitle, ogDescription, ogImage *string) error {
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
	if len(sets) == 0 {
		return nil
	}

	args = append(args, code)
	_, err := db.Exec("UPDATE urls SET "+strings.Join(sets, ", ")+" WHERE code = ?", args...)
	return err
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
