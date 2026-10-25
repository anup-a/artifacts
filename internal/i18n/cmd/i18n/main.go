// Command i18n reports what the catalogs lack, run from the repository root.
//
//	go run ./internal/i18n/cmd/i18n            missing messages per language
//	go run ./internal/i18n/cmd/i18n zh-CN      zh-CN's missing messages as JSON, ready to translate
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/anup-a/byagent/internal/i18n"
)

func main() {
	msgs, err := i18n.Collect(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(os.Args) > 1 {
		todo := map[string]string{}
		for _, m := range i18n.Missing(os.Args[1], msgs) {
			todo[m] = ""
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		_ = enc.Encode(todo)
		return
	}
	fmt.Printf("%d messages\n", len(msgs))
	for _, l := range i18n.Locales[1:] {
		fmt.Printf("%-6s %d missing\n", l.Code, len(i18n.Missing(l.Code, msgs)))
	}
}
