package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var fox = "The quick brown fox jumps over the lazy dog"

func TestWordWrapMultiLine(t *testing.T) {
	words := strings.Fields(fox)
	wrapped := WordWrap(words, 10)
	require.Equal(t, 5, len(wrapped))
	require.Equal(t, "The quick", wrapped[0])
	require.Equal(t, "brown fox", wrapped[1])
	require.Equal(t, "jumps over", wrapped[2])
	require.Equal(t, "the lazy", wrapped[3])
	require.Equal(t, "dog", wrapped[4])
}

func TestWordWrapSingleLine(t *testing.T) {
	words := strings.Fields(fox)
	wrapped := WordWrap(words, 100)
	require.Equal(t, 1, len(wrapped))
	require.Equal(t, fox, wrapped[0])
}

func TestWordWrapTruncate(t *testing.T) {
	words := strings.Fields(fox)
	wrapped := WordWrap(words, 3)
	require.Equal(t, 9, len(wrapped))
	require.Equal(t, "The", wrapped[0])
	require.Equal(t, "qui", wrapped[1])
	require.Equal(t, "bro", wrapped[2])
	require.Equal(t, "fox", wrapped[3])
	require.Equal(t, "jum", wrapped[4])
	require.Equal(t, "ove", wrapped[5])
	require.Equal(t, "the", wrapped[6])
	require.Equal(t, "laz", wrapped[7])
	require.Equal(t, "dog", wrapped[8])
}
