// Command schemagen prints the configuration schema without using the embedded copy.
package main

import (
	"fmt"
	"github.com/lorem-dev/duolog/internal/config/schemagen"
	"os"
)

func main() {
	data, err := schemagen.Generate()
	if err == nil {
		_, err = os.Stdout.Write(data)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
