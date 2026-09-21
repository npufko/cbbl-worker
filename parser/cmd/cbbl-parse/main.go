// cbbl-parse: demo file in, normalized JSON out. No network, no database (plan §9.1).
// Runs on GitHub Actions in the public worker repo — never on end-user PCs.
//
//	cbbl-parse --in match.dem.zst --out map.json
//
// Accepts raw .dem, .dem.zst (FACEIT), .dem.bz2 (Valve) and .dem.gz by magic bytes.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	dem "github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/msg"

	"github.com/cbbl/parser/internal/demofile"
	"github.com/cbbl/parser/internal/stats"
)

func main() {
	in := flag.String("in", "", "demo file (.dem, .dem.zst, .dem.bz2, .dem.gz)")
	out := flag.String("out", "-", "output JSON path, - for stdout")
	flag.Parse()
	if *in == "" {
		fail("missing --in")
	}

	r, closeFn, err := demofile.Open(*in)
	if err != nil {
		fail("open: %v", err)
	}
	defer closeFn()

	cfg := dem.DefaultParserConfig
	// Tolerate known CS2 demo quirks instead of aborting the whole match.
	cfg.IgnoreErrBombsiteIndexNotFound = true
	p := dem.NewParserWithConfig(r, cfg)
	defer p.Close()

	// v5 does not expose the demo header on the Parser interface; read the map from ServerInfo.
	mapName := ""
	p.RegisterNetMessageHandler(func(m *msg.CSVCMsg_ServerInfo) { mapName = m.GetMapName() })

	c := stats.NewCollector(p)
	if err := p.ParseToEnd(); err != nil {
		// A truncated tail is common; keep what we have if rounds were parsed.
		if len(c.Rounds()) == 0 {
			fail("parse: %v", err)
		}
		fmt.Fprintf(os.Stderr, "warning: parse ended early: %v\n", err)
	}

	result := c.Result(mapName)
	w := os.Stdout
	if *out != "-" {
		if w, err = os.Create(*out); err != nil {
			fail("create out: %v", err)
		}
		defer w.Close()
	}
	enc := json.NewEncoder(w)
	if err := enc.Encode(result); err != nil {
		fail("encode: %v", err)
	}
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "cbbl-parse: "+format+"\n", a...)
	os.Exit(1)
}
