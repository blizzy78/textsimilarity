package io

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"github.com/matryer/is"
)

func TestReadLine(t *testing.T) {
	is := is.New(t)

	givenLine := "test"
	givenLine2 := strings.Repeat("verylongline", 1024)

	r := bufio.NewReader(strings.NewReader(givenLine + "\n" + givenLine2 + "\n"))
	buf := bytes.Buffer{}

	line, _ := ReadLine(r, &buf)
	is.Equal(line, givenLine)
	line, _ = ReadLine(r, &buf)
	is.Equal(line, givenLine2)
}
