package main

// This trick is learnt from a post by Rob Pike
// https://groups.google.com/d/msg/golang-nuts/gU7oQGoCkmg/j3nNxuS2O_sJ

// For error message, use log pkg directly

import (
	"log"
	"os"
)

const info infoLogging = true
const debug debugLogging = true
const errl errorLogging = true

type infoLogging bool

func (d infoLogging) Printf(format string, args ...interface{}) {
	if d {
		log.Printf(format, args...)
	}
}

func (d infoLogging) Println(args ...interface{}) {
	if d {
		log.Println(args...)
	}
}

// debug logging
type debugLogging bool

var debugLog = log.New(os.Stderr, "\033[34m[DEBUG ", log.LstdFlags)

func (d debugLogging) Printf(format string, args ...interface{}) {
	if d {
		debugLog.Printf("]\033[0m "+format, args...)
	}
}

type errorLogging bool

var errorLog = log.New(os.Stderr, "\033[31m[ERROR ", log.LstdFlags)

func (d errorLogging) Printf(format string, args ...interface{}) {
	if d {
		errorLog.Printf("]\033[0m "+format, args...)
	}
}
