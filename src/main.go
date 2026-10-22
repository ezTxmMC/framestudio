package main

import (
	"os"

	"framestudio-proto/src/application"
)

func main() {
	application.Run(os.Args[1:])
}
