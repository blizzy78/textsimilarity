package main

import (
	"flag"
	"io"
	"os"
	"testing"

	"github.com/blizzy78/textsimilarity"
)

func TestMaxLineDistanceOption(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"default", nil, textsimilarity.DefaultMaxLineDistance},
		{"strict", []string{"-maxLineDist=0"}, 0},
		{"custom", []string{"-maxLineDist=7"}, 7},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			originalFlags, originalArgs := flag.CommandLine, os.Args

			t.Cleanup(func() {
				flag.CommandLine, os.Args = originalFlags, originalArgs
			})
			flag.CommandLine = flag.NewFlagSet(t.Name(), flag.ContinueOnError)
			flag.CommandLine.SetOutput(io.Discard)

			os.Args = append([]string{"textsimilarity"}, test.args...)
			os.Args = append(os.Args, "input.txt")

			opts, err := options()
			if err != nil {
				t.Fatal(err)
			}

			if flag.Lookup("maxDist") != nil {
				t.Fatal("obsolete maxDist flag is still registered")
			}

			if opts.simOpts.MaxLineDistance != test.want {
				t.Fatalf("line distance: got %d, want %d", opts.simOpts.MaxLineDistance, test.want)
			}
		})
	}
}
