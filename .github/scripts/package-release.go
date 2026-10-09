// Command package-release zips one plugin library at the archive root and writes its
// sha256sum line, in the layout the CLIProxyAPI plugin installer expects.
package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	library := flag.String("library", "", "path to the built plugin library")
	archive := flag.String("archive", "", "zip file to create")
	checksum := flag.String("checksum", "", "sha256sum line file to create")
	flag.Parse()
	if *library == "" || *archive == "" || *checksum == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*library, *archive, *checksum); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(library, archive, checksum string) error {
	if err := writeZip(library, archive); err != nil {
		return err
	}
	sum, err := sha256File(archive)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("%s  %s\n", sum, filepath.Base(archive))
	return os.WriteFile(checksum, []byte(line), 0o644)
}

func writeZip(library, archive string) (err error) {
	out, err := os.Create(archive)
	if err != nil {
		return err
	}
	defer func() {
		if errClose := out.Close(); err == nil {
			err = errClose
		}
	}()

	in, err := os.Open(library)
	if err != nil {
		return err
	}
	defer func() {
		if errClose := in.Close(); err == nil {
			err = errClose
		}
	}()

	info, err := in.Stat()
	if err != nil {
		return err
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = filepath.Base(library)
	header.Method = zip.Deflate

	zw := zip.NewWriter(out)
	entry, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	if _, err = io.Copy(entry, in); err != nil {
		return err
	}
	return zw.Close()
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
