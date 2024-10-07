package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kingpin/v2"
)

var (
	app      = kingpin.New("greeter", "A simple greeting application")
	name     = app.Flag("name", "Name to greet").Default("World").String()
	greeting = app.Flag("greeting", "Greeting to use").Default("Hello").String()
)

func main() {
	kingpin.MustParse(app.Parse(os.Args[1:]))

	fmt.Printf("%s, %s!\n", *greeting, *name)
}
