/*
Copyright 2020 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tar

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// Compress the provided  `tarContentsPath` into the `tarFilePath` while
// excluding the `exclude` regular expression patterns.
func Compress(tarFilePath, tarContentsPath string, excludes ...*regexp.Regexp) error {
	tarFile, err := os.Create(tarFilePath)
	if err != nil {
		return errors.Wrapf(err, "create tar file %q", tarFilePath)
	}
	defer tarFile.Close()

	gzipWriter := gzip.NewWriter(tarFile)
	defer gzipWriter.Close()

	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	if err := filepath.Walk(tarContentsPath, func(filePath string, fileInfo os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		var link string
		isLink := fileInfo.Mode()&os.ModeSymlink == os.ModeSymlink
		if isLink {
			link, err = os.Readlink(filePath)
			if err != nil {
				return errors.Wrapf(err, "read file link of %s", filePath)
			}
		}

		header, err := tar.FileInfoHeader(fileInfo, link)
		if err != nil {
			return errors.Wrapf(err, "create file info header for %q", filePath)
		}

		if fileInfo.IsDir() || filePath == tarFilePath {
			logrus.Debugf("Skipping: %s", filePath)
			return nil
		}

		for _, re := range excludes {
			if re != nil && re.MatchString(filePath) {
				logrus.Debugf("Excluding: %s", filePath)
				return nil
			}
		}

		// Make the path inside the tar relative to the archive path if
		// necessary.
		header.Name = strings.TrimLeft(
			strings.TrimPrefix(filePath, filepath.Dir(tarFilePath)),
			string(filepath.Separator),
		)
		header.Linkname = filepath.ToSlash(header.Linkname)

		if err := tarWriter.WriteHeader(header); err != nil {
			return errors.Wrap(err, "writing tar header")
		}

		if !isLink {
			file, err := os.Open(filePath)
			if err != nil {
				return errors.Wrapf(err, "open file %q", filePath)
			}

			if _, err := io.Copy(tarWriter, file); err != nil {
				return errors.Wrap(err, "writing file to tar writer")
			}

			file.Close()
		}

		return nil
	}); err != nil {
		return errors.Wrapf(err, "walking tree in %q", tarContentsPath)
	}

	return nil
}

// ReadFileFromGzippedTar opens a tarball and reads contents of a file inside.
func ReadFileFromGzippedTar(
	tarPath, filePath string,
) (res io.Reader, err error) {
	if err := iterateTarball(
		tarPath,
		func(reader *tar.Reader, header *tar.Header) (stop bool) {
			if header.Name == filePath {
				res = reader
				return true
			}
			return false
		},
	); err != nil {
		return nil, err
	}

	if res == nil {
		return nil, errors.Errorf(
			"unable to find file %q in tarball %q", tarPath, filePath,
		)
	}

	return res, nil
}

// iterateTarball can be used to iterate over the contents of a tarball by
// calling the callback for each entry.
func iterateTarball(
	tarPath string,
	callback func(*tar.Reader, *tar.Header) (stop bool),
) error {
	file, err := os.Open(tarPath)
	if err != nil {
		return errors.Wrapf(err, "opening tar file %q", tarPath)
	}

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return errors.Wrapf(err, "creating gzip reader for file %q", tarPath)
	}
	tarReader := tar.NewReader(gzipReader)

	for {
		tarHeader, err := tarReader.Next()
		if err == io.EOF {
			break // End of archive
		}

		if callback(tarReader, tarHeader) {
			// User wants to stop
			break
		}
	}

	return nil
}
