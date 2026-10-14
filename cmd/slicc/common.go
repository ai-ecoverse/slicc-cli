package main

import (
	"math/rand/v2"
	"strings"
	"time"
)

const (
	followBackoffBase = time.Second
	followBackoffMax  = 30 * time.Second
)

func jitter(d time.Duration) time.Duration {
	return d*3/4 + time.Duration(rand.Int64N(int64(d)/2+1))
}

func looksLikeURL(arg string) bool {
	return strings.Contains(arg, "://")
}
