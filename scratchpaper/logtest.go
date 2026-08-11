package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"

	"github.com/fhs/gompd/v2/mpd"
)

const (
	logfile   = "/var/log/mpd/mpd.log"
	mpdSocket = "/run/mpd/socket"
)

func main() {

	f, err := os.Open(logfile)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	re := regexp.MustCompile(`player: .*?"([^"]+)"`)

	paths := map[string]bool{}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {

		line := scanner.Text()

		m := re.FindStringSubmatch(line)
		if len(m) > 1 {
			paths[m[1]] = true
		}
	}

	fmt.Println("unique paths:", len(paths))

	c, err := mpd.Dial("unix", mpdSocket)
	if err != nil {
		panic(err)
	}
	defer c.Close()

	ok := 0
	fail := 0

	for p := range paths {

		_, err := c.ListAllInfo(p)

		if err != nil {
			fail++
			fmt.Println("FAIL:", p, err)
		} else {
			ok++
		}
	}

	fmt.Println("queries ok:", ok)
	fmt.Println("queries fail:", fail)
}
