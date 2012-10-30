/*
  Copyright (c) 2012 José Carlos Nieto, http://xiam.menteslibres.org/

  Permission is hereby granted, free of charge, to any person obtaining
  a copy of this software and associated documentation files (the
  "Software"), to deal in the Software without restriction, including
  without limitation the rights to use, copy, modify, merge, publish,
  distribute, sublicense, and/or sell copies of the Software, and to
  permit persons to whom the Software is furnished to do so, subject to
  the following conditions:

  The above copyright notice and this permission notice shall be
  included in all copies or substantial portions of the Software.

  THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
  EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
  MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND
  NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE
  LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION
  OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION
  WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
*/

package main

import (
	"bytes"
	"crypto"
	"flag"
	"fmt"
	"github.com/gosexy/checksum"
	"github.com/gosexy/exif"
	"github.com/gosexy/to"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode"
)

const PS = string(os.PathSeparator)

var pcount = 0

var ok chan int

var statsCopied int
var statsMoved int
var statsSkipped int
var statsNotExif int

var flagFrom = flag.String("from", "", "Media source directory.")
var flagDest = flag.String("to", "", "Media destination directory.")
var flagMove = flag.Bool("move", false, "Delete original file after copying (rename file).")
var flagDryRun = flag.Bool("dry-run", false, "Prints what would be done without actually doing it.")
var flagMaxProcs = flag.Int("max-procs", runtime.NumCPU(), "The maximum number of tasks running at the same time.")
var flagExifTool = flag.Bool("exiftool", false, "Use exiftool instead of libexif (slower. requires exiftool to be installed).")
var flagTryExifTool = flag.Bool("try-exiftool", false, "Fallback to exiftool if libexif fails (requires exiftool to be installed).")

func getExifData(file string) (map[string]string, error) {
	var err error

	if *flagExifTool == false || *flagTryExifTool == true {

		ex := exif.New()
		err = ex.Open(file)

		if err == nil {
			return ex.Tags, nil
		}

	}

	if *flagExifTool == true || *flagTryExifTool == true {

		cmd := exec.Command("exiftool", file)

		var out bytes.Buffer
		cmd.Stdout = &out

		err := cmd.Run()

		if err != nil {
			return nil, err
		}

		tags := make(map[string]string)

		data := strings.Trim(out.String(), " \r\n")
		lines := strings.Split(data, "\n")

		for _, line := range lines {
			key := strings.Trim(line[0:32], " ")
			value := strings.Trim(line[33:], " ")
			tags[key] = value
		}

		return tags, nil
	}

	return nil, fmt.Errorf("Could not read EXIF data.")
}

func verifyDirectory(name string) error {
	stat, err := os.Stat(name)
	if err != nil {
		return err
	}
	if stat.IsDir() == false {
		return fmt.Errorf("%s: is not a directory.", *flagFrom)
	}
	return nil
}

func Copy(src string, dst string) error {
	var err error

	input, err := os.Open(src)
	if err != nil {
		return err
	}
	defer input.Close()

	output, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer output.Close()

	_, err = io.Copy(output, input)

	return err
}

func Move(src string, dst string) error {
	var err error

	err = os.Rename(src, dst)

	if err != nil {

		err = Copy(src, dst)

		if err != nil {
			return err
		}

		return os.Remove(src)
	}

	return nil
}

func textilize(input string) string {
	var re *regexp.Regexp

	sc := unicode.SpecialCase{}

	output := strings.ToLowerSpecial(sc, input)

	re, _ = regexp.Compile("[áäâãà]")
	output = re.ReplaceAllLiteralString(output, "a")

	re, _ = regexp.Compile("[éëêẽè]")
	output = re.ReplaceAllLiteralString(output, "e")

	re, _ = regexp.Compile("[íïîĩì]")
	output = re.ReplaceAllLiteralString(output, "i")

	re, _ = regexp.Compile("[óöôõò]")
	output = re.ReplaceAllLiteralString(output, "o")

	re, _ = regexp.Compile("[úüûũù]")
	output = re.ReplaceAllLiteralString(output, "u")

	re, _ = regexp.Compile("[ñ]")
	output = re.ReplaceAllLiteralString(output, "n")

	re, _ = regexp.Compile("[^a-z0-9]")
	output = re.ReplaceAllLiteralString(output, " ")

	re, _ = regexp.Compile(" +")
	output = re.ReplaceAllLiteralString(output, " ")

	output = strings.Replace(strings.TrimSpace(output), " ", "_", -1)

	return output
}

func normalize(chunks ...string) string {
	name := []string{}
	for _, chunk := range chunks {
		chunk = strings.Trim(chunk, "")
		if chunk != "" {
			name = append(name, textilize(chunk))
		}
	}
	return strings.Join(name, "-")
}

func pick(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func Import(name string, dest string) {

	defer func() {
		ok <- 1
	}()

	re, _ := regexp.Compile(`(\d{4}):(\d{2}):(\d{2}) (\d{2}):(\d{2}):(\d{2})`)

	tags, err := getExifData(name)

	if err == nil {

		rename := ""

		switch tags["File Type"] {

		case "MP3":

			hash := checksum.File(name, crypto.SHA1)

			rename = strings.Join(
				[]string{
					dest,
					normalize(pick(tags["Artist"], "Unknown Artist")),
					normalize(pick(tags["Album"], "Unknown Album")),
					fmt.Sprintf("%s%s", normalize(tags["Track"], fmt.Sprintf("%s-%s", pick(tags["Title"], "Unknown Title"), hash[0:4])), pick(strings.ToLower(path.Ext(name)), ".mp3")),
				},
				PS,
			)

		default:
			var taken string

			dateTimeFields := []string{
				"Date and Time (Original)",
				"Date/Time Original",
				"Media Create Date",
				"Track Create Date",
				"Create Date",
			}

			for _, field := range dateTimeFields {
				if tags[field] != "" {
					taken = tags[field]
					break
				}
			}

			if taken == "" {
				statsNotExif++
				return
			}

			all := re.FindAllStringSubmatch(taken, -1)

			timeTaken := time.Date(
				to.Int(all[0][1]),
				time.Month(to.Int(all[0][2])),
				to.Int(all[0][3]),
				to.Int(all[0][4]),
				to.Int(all[0][5]),
				to.Int(all[0][6]),
				0,
				time.UTC,
			)

			hash := checksum.File(name, crypto.SHA1)

			rename = strings.Join(
				[]string{
					dest,
					to.String(timeTaken.Year()),
					fmt.Sprintf("%02d-%s", timeTaken.Month(), timeTaken.Month()),
					fmt.Sprintf("%02d-%s", timeTaken.Day(), timeTaken.Weekday()),
					fmt.Sprintf("%02d%02d%02d-%s%s", timeTaken.Hour(), timeTaken.Minute(), timeTaken.Second(), strings.ToUpper(hash[0:4]), strings.ToLower(path.Ext(name))),
				},
				PS,
			)
		}

		if rename != "" {

			_, err := os.Stat(rename)

			if err != nil {

				if *flagDryRun == false {
					err = os.MkdirAll(path.Dir(rename), os.ModeDir|0750)
					if err != nil {
						panic(err)
					}
				}
				err = nil
				if *flagMove == true {
					log.Printf("Moving file: %s -> %s\n", name, rename)
					if *flagDryRun == false {
						err = Move(name, rename)
						statsMoved++
					}
				} else {
					log.Printf("Copying file: %s -> %s\n", name, rename)
					if *flagDryRun == false {
						err = Copy(name, rename)
						statsCopied++
					}
				}
				if err != nil {
					panic(err)
				}

			} else {
				log.Printf("Skipping file: %s\n", rename)
				statsSkipped++
			}

		} else {
			statsNotExif++
		}

	} else {
		statsNotExif++
	}

}

func Scandir(dirname string, dest string) error {

	var err error

	stat, err := os.Stat(dirname)

	if err != nil {
		return err
	}

	if stat.IsDir() == false {
		return fmt.Errorf("Not a directory.")
	}

	dh, err := os.Open(dirname)

	if err != nil {
		return err
	}

	defer dh.Close()

	files, err := dh.Readdir(-1)

	if err != nil {
		return err
	}

	for _, file := range files {

		name := dirname + PS + file.Name()

		if file.IsDir() == true {
			Scandir(name, dest)
		} else {
			if pcount >= *flagMaxProcs {
				// Waiting for one task to finish
				<-ok
				pcount--
			}
			go Import(name, dest)
			// Task count
			pcount++
		}

	}

	return nil
}

func main() {

	flag.Parse()

	if *flagFrom == "" || *flagDest == "" {
		fmt.Printf("Photopy, by xiam <xiam@menteslibres.org>, Mexico City.\n\n")
		fmt.Printf("A command line tool for importing photos.\n\n")
		fmt.Printf("Sample usage:\n\n\tphotopy -from /media/usb/DCIM -to ~/Photos -dry-run\n\n")
		flag.PrintDefaults()
		fmt.Println("")
	} else {
		var err error

		ok = make(chan int, *flagMaxProcs)

		err = verifyDirectory(*flagFrom)
		if err != nil {
			log.Println(err.Error())
			return
		}

		err = verifyDirectory(*flagDest)
		if err != nil {
			log.Println(err.Error())
			return
		}

		Scandir(*flagFrom, *flagDest)

		// Waiting for all tasks to finish
		for i := 0; i < pcount; i++ {
			<-ok
		}

		fmt.Printf("Copied: %d, Moved: %d, Skipped: %d, Without EXIF data: %d\n", statsCopied, statsMoved, statsSkipped, statsNotExif)
	}
}
