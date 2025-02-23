package utils

import (
	"fmt"
	"io"
	"log"
)

func ConfigureLogger(prefix string, writer io.Writer) {
	log.SetPrefix(fmt.Sprintf("%s ", prefix))
	log.SetOutput(writer)
}
