// cbbl-parse: demo file in, normalized JSON out. No network, no database (plan §9.1).
// Runs on GitHub Actions in the public worker repo — never on end-user PCs.
//
//	cbbl-parse --in match.dem.zst --out map.json
//	cbbl-parse --in map_4386.dem --in map_4386_1.dem --out map.json   (one map, two recording segments)
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

// segments collects repeated --in flags: the recording segments of ONE map, in play order.
type segments []string

func (s *segments) String() string     { return fmt.Sprint(*s) }
func (s *segments) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	var ins segments
	flag.Var(&ins, "in", "demo file (.dem, .dem.zst, .dem.bz2, .dem.gz); repeat for the recording segments of one map, in order")
	out := flag.String("out", "-", "output JSON path, - for stdout")
	flag.Parse()
	if len(ins) == 0 {
		fail("missing --in")
	}

	cfg := dem.DefaultParserConfig
	// Tolerate known CS2 demo quirks instead of aborting the whole match.
	cfg.IgnoreErrBombsiteIndexNotFound = true

	var c *stats.Collector
	mapName := ""
	for i, in := range ins {
		r, closeFn, err := demofile.Open(in)
		if err != nil {
			fail("open %s: %v", in, err)
		}
		defer closeFn()
		p := dem.NewParserWithConfig(r, cfg)
		defer p.Close() // kept open to the end: the result reads the last segment's final game state

		// v5 does not expose the demo header on the Parser interface; read the map from ServerInfo.
		p.RegisterNetMessageHandler(func(m *msg.CSVCMsg_ServerInfo) {
			if n := m.GetMapName(); n != "" {
				mapName = n
			}
		})
		if i == 0 {
			c = stats.NewCollector(p)
		} else {
			c.Continue(p)
		}
		if err := p.ParseToEnd(); err != nil {
			// A truncated tail is common; keep what we have if rounds were parsed.
			if len(c.Rounds()) == 0 && i == len(ins)-1 {
				fail("parse %s: %v", in, err)
			}
			fmt.Fprintf(os.Stderr, "warning: %s: parse ended early: %v\n", in, err)
		}
	}

	result := c.Result(mapName)
	w := os.Stdout
	if *out != "-" {
		var err error
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
