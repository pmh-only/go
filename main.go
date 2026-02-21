package main

import (
	"log"
	"net/http"

	_ "modernc.org/sqlite"
)

const (
	charset = "abcdefghkprstxyz2345678"
	codeLen = 6
)

func main() {
	if err := initDB(); err != nil {
		log.Fatalf("failed to init database: %v", err)
	}
	defer db.Close()

	if err := loadSettings(); err != nil {
		log.Fatalf("failed to load settings: %v", err)
	}

	pb, ph, uh, ih, ah := cfg.snapshot()
	log.Printf("public: %s (%s)  ui: %s  internal: %s  alias: %s", pb, ph, uh, ih, ah)

	http.HandleFunc("/", mainHandler)
	log.Fatal(http.ListenAndServe(port, nil))
}
