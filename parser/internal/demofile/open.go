// Package demofile opens raw, zstd (FACEIT), bzip2 (Valve) or gzip demos by magic bytes.
package demofile

import (
	"bufio"
	"compress/bzip2"
	"compress/gzip"
	"io"
	"os"

	"github.com/klauspost/compress/zstd"
)

// Open returns a decompressed stream and a close func.
func Open(path string) (io.Reader, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	br := bufio.NewReaderSize(f, 1<<20)
	magic, err := br.Peek(4)
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	switch {
	case magic[0] == 0x28 && magic[1] == 0xB5 && magic[2] == 0x2F && magic[3] == 0xFD:
		d, err := zstd.NewReader(br)
		if err != nil {
			f.Close()
			return nil, nil, err
		}
		return d, func() { d.Close(); f.Close() }, nil
	case magic[0] == 'B' && magic[1] == 'Z' && magic[2] == 'h':
		return bzip2.NewReader(br), func() { f.Close() }, nil
	case magic[0] == 0x1F && magic[1] == 0x8B:
		g, err := gzip.NewReader(br)
		if err != nil {
			f.Close()
			return nil, nil, err
		}
		return g, func() { g.Close(); f.Close() }, nil
	default:
		return br, func() { f.Close() }, nil
	}
}
