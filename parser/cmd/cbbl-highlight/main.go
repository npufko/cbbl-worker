// cbbl-highlight: pick each player's best round from a demo and render a 2D replay clip (MP4).
//
//	cbbl-highlight --in match.dem.zst --list                    # JSON: every player's best round
//	cbbl-highlight --in match.dem.zst --steam best --out a.mp4  # the match's top play
//	cbbl-highlight --in match.dem.zst --steam 7656119... --out puf.mp4
//
// Runs on GitHub Actions next to cbbl-parse (ffmpeg is on the runner). Never on end-user PCs.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"

	dem "github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs"
	"github.com/markus-wa/demoinfocs-golang/v5/pkg/demoinfocs/msg"

	"github.com/cbbl/parser/internal/demofile"
	"github.com/cbbl/parser/internal/replay"
)

func main() {
	in := flag.String("in", "", "demo file")
	list := flag.Bool("list", false, "print highlights as JSON and exit")
	steam := flag.String("steam", "best", "SteamID64 to render, or 'best'")
	out := flag.String("out", "highlight.mp4", "output MP4")
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
	cfg.IgnoreErrBombsiteIndexNotFound = true
	p := dem.NewParserWithConfig(r, cfg)
	defer p.Close()
	mapName := ""
	p.RegisterNetMessageHandler(func(m *msg.CSVCMsg_ServerInfo) { mapName = m.GetMapName() })
	rec := replay.NewRecorder(p, 2)
	if err := p.ParseToEnd(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: parse ended early: %v\n", err)
	}
	res := rec.Result(mapName)
	hs := replay.Highlights(res)
	if len(hs) == 0 {
		fail("no highlights (no kills in live rounds?)")
	}
	if *list {
		_ = json.NewEncoder(os.Stdout).Encode(hs)
		return
	}
	pick := hs[0]
	if *steam != "best" {
		id, err := strconv.ParseUint(*steam, 10, 64)
		if err != nil {
			fail("bad --steam")
		}
		found := false
		for _, h := range hs {
			if h.SteamID == id {
				pick, found = h, true
			}
		}
		if !found {
			fail("player %s has no kills in this demo", *steam)
		}
	}
	if err := replay.Render(res, pick, *out); err != nil {
		fail("render: %v", err)
	}
	fmt.Printf("%s: %s R%d (%.1fs) -> %s\n", pick.Name, pick.Label, pick.Round, float64(pick.ToTick-pick.FromTick)/res.TickRate, *out)
}

func fail(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "cbbl-highlight: "+f+"\n", a...)
	os.Exit(1)
}
