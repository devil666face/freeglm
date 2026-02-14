package main

import (
	"fmt"
	"os"

	"freeglm/internal/freeglm"
)

func main() {
	_freeglm, err := freeglm.New()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if err := _freeglm.Start(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
