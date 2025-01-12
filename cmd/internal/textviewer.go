package internal

import (
	"bytes"
	"os"
	"os/exec"
	"strings"

	"github.com/pkg/errors"
)

func ViewInEditor(text string) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		return errors.New("Please define a text editor to use with the $EDITOR environment variable")
	}

	tmpfile, err := os.CreateTemp("", "prom-scrape-analyzer-*.txt")
	defer func() {
		if tmpfile != nil {
			_ = tmpfile.Close()
			_ = os.Remove(tmpfile.Name())
		}
	}()
	if err != nil {
		return errors.Wrap(err, "failed to create temporary file to display text")
	}

	_, err = tmpfile.WriteString(text)
	if err != nil {
		return errors.Wrap(err, "failed to write text to temporary file")
	}

	args := strings.Split(editor, " ")
	args = append(args, tmpfile.Name())

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		return err
	}
	if stderr.Len() > 0 {
		return errors.New(stderr.String())
	}

	return nil
}
