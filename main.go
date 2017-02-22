package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
)

const windowSize = 4096

func init() {
	log.SetFlags(log.LstdFlags | log.LUTC | log.Lshortfile)
}

func handleErr(err error) {
	if err != nil {
		log.Output(2, err.Error())
		os.Exit(1)
	}
}

// read a single line, check if it ends with .json or not
// if it does, hand it off to be checked
func handleOutput(r io.Reader, wg *sync.WaitGroup, c chan struct{}) {
	defer wg.Done()

	b := bufio.NewReader(r)
	line, err := b.ReadString('\n')
	// end of file is not an error
	if err != nil && err == io.EOF {
		return
	}

	handleErr(err)

	line = strings.TrimSpace(line)
	if len(line) < 5 || line[len(line)-len(".json"):] != ".json" {
		return
	}

	wg.Add(1)
	go handleFile(line, wg, c)
}

// checks if a file is valid json, signals this fact on the channel
// outputs an error with a minimal context on stdout
func handleFile(path string, wg *sync.WaitGroup, c chan struct{}) {
	defer wg.Done()

	f, err := os.OpenFile(path, os.O_RDONLY, 0660)
	handleErr(err)
	defer f.Close()

	var m map[string]interface{}
	dec := json.NewDecoder(f)
	decerr := dec.Decode(&m)
	if decerr == nil {
		return
	}

	c <- struct{}{}

	var offset int64
	if err, ok := decerr.(*json.SyntaxError); ok {
		offset = err.Offset
	}

	if offset == 0 {
		log.Printf("%v: %v\n", path, err.Error())
		return
	}

	fi, err := f.Stat()
	handleErr(err)

	size := fi.Size()
	var start int64
	end := size - offset
	if offset-(windowSize/2) > 0 {
		start = offset - (windowSize / 2)
	}
	if start+4096 < size {
		end = start + windowSize
	}

	_, err = f.Seek(start, 0)
	handleErr(err)

	buf := make([]byte, end)
	_, err = io.ReadFull(f, buf)
	handleErr(err)

	buf = getWindow(buf, offset-start)

	log.Printf("%v:%v: %v; context:\n%v\n\n", path, offset, decerr.Error(), string(buf))
}

// extracts the context from the given buffer
func getWindow(buf []byte, offset int64) []byte {
	var newlines int
	var snl, enl int64
	for snl = offset; snl > 0; snl-- {
		if buf[snl] != '\n' {
			continue
		}

		newlines++
		if newlines == 3 {
			break
		}
	}

	newlines = 0
	for enl = offset; snl < int64(len(buf)); enl++ {
		if buf[enl] != '\n' {
			continue
		}

		newlines++
		if newlines == 2 {
			break
		}
	}

	return bytes.Trim(buf[snl:enl], "\r\n")
}

func main() {
	c := make(chan struct{})
	wg := &sync.WaitGroup{}

	cmd := exec.Command(
		"git",
		"diff-index",
		"--cached",
		"--name-only",
		"--diff-filter=ACdMRTUXB",
		"HEAD",
	)
	stdout, err := cmd.StdoutPipe()
	handleErr(err)

	wg.Add(1)
	go handleOutput(stdout, wg, c)

	err = cmd.Start()
	handleErr(err)

	exitcode := 0
	quit := make(chan struct{})
	go (func() {
		for range c {
			exitcode = 1
		}

		close(quit)
	})()

	wg.Wait() // wait for everything to finish
	close(c)  // close the error signalling channel
	<-quit    // wait until the signalling channels has finished processing
	os.Exit(exitcode)
}
