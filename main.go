package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/jpeg"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	modelDir = "__models__"
)

type tModel struct {
	name    string // model's file name (will be appended to recovered file)
	header  []byte // initial data of the model file (all until last SOS marker 0xFFDA, excluding the marker)
	sosBloc []byte // SOS marker data bloc (used in case the corrupted file has no SOS marker)
}

var (
	gModels []tModel
	gRegExp = regexp.MustCompile(`\.id_(\d+?)_(.+?)\.onion\._`) // remove ransomware file extension
)

func main() {
	loadModels()

	// jpeg save quality
	var opt jpeg.Options
	opt.Quality = 98

	flag.Parse()
	for i := 0; i < flag.NArg(); i++ {
		filePath := flag.Arg(i)
		fmt.Printf("\n------------------------------------------------------------------------\nFile: %s\n", filepath.Base(filePath))

		if firstOptions(filePath) {
			fmt.Printf(">>>> Done!\n\n")
			continue
		}

		// load data from the corrupted file
		fileData := loadFile(filePath)

		for _, model := range gModels {
			fmt.Printf(">> Model: %s\n", model.name)

			// merge model and file data
			img, err := model.appendFileData(fileData)
			if err != nil {
				fmt.Printf(">>>> Error: %v\n\n", err)
				continue
			}

			// create new file
			newFilePath := gRegExp.ReplaceAllString(filePath+model.name, "-")
			hf, err := os.Create(newFilePath)
			if err != nil {
				panic(err)
			}

			// encode new file
			if err := jpeg.Encode(hf, img, &opt); err != nil {
				fmt.Printf(">>>> Error: %v\n", err)
			} else {
				fmt.Printf(">>>> Done: %s\n", filepath.Base(newFilePath))
			}

			fmt.Printf("\n")
			hf.Close()
		}
	}

	fmt.Printf("------------------------------------------------------------------------\nPress enter to exit...")
	fmt.Scanln()
}

func firstOptions(file string) bool {
	data, err := ioutil.ReadFile(file)
	if err != nil {
		return false
	}

	data = data[10240:]
	data = data[:len(data)-36]

	// 01: search for a 0xFFE0 marker in the file (last embeeded image, it should be the good one, not an embeeded thumbnail)
	if id := bytes.LastIndex(data, []byte{0xFF, 0xE0}); id != -1 {
		fmt.Printf(">> Option 01: 0xFFE0 found after 10kb\n")
		newFile := gRegExp.ReplaceAllString(file, "") + "-option_FFE0.jpg"
		hf, err := os.Create(newFile)
		if err != nil {
			fmt.Printf(">>>> Error: %v\n\n", err)
			return false
		}
		hf.Write([]byte{0xFF, 0xD8})
		hf.Write(data[id:])
		hf.Close()

		if askForConfirmation(fmt.Sprintf(">>>> %s created. Is it valid?", filepath.Base(newFile))) {
			return true
		}
	}

	// 02: find a 0xFFD9 (EOF) marker that is not the last
	if idLast := bytes.LastIndex(data, []byte{0xFF, 0xD9}); idLast != -1 {
		if id := bytes.LastIndex(data[:idLast-10], []byte{0xFF, 0xD9}); id != -1 {
			fmt.Printf(">> Option 02: Found an EOF marker that is not the last one\n")

			data = data[id+2:]

			for {
				id = bytes.Index(data, []byte{0xFF})
				if id == -1 {
					break
				}

				data = data[id+1:]
				if data[0] >= 0xC0 && data[0] <= 0xDF {
					break
				}
			}

			if id == -1 {
				fmt.Printf(">>            No valid marker after EOF\n\n")
				return false
			}

			fmt.Printf(">>            Found valid marker after EOF (0x%02X%02X)", 0xFF, data[0])

			newFile := gRegExp.ReplaceAllString(file, "") + "-option_FFD9.jpg"
			hf, err := os.Create(newFile)
			if err != nil {
				fmt.Printf(">>>> Error: %v\n\n", err)
				return false
			}
			hf.Write([]byte{0xFF, 0xD8, 0xFF})
			hf.Write(data)
			hf.Close()

			if askForConfirmation(fmt.Sprintf(">>>> %s created. Is it valid?", filepath.Base(newFile))) {
				return true
			}
		}
	}

	return false
}

func (m *tModel) appendFileData(fileData []byte) (img image.Image, err error) {
	var data []byte
	// if file data already has a SOS marker, then juste merge model header and file data
	if fileData[0] == 0xFF && fileData[1] == 0xDA {
		data = append(m.header, fileData...)
	} else {
		// merge: model header / model SOS bloc / file data
		data = append(m.header, m.sosBloc...)
		data = append(data, fileData...)
	}
	return jpeg.Decode(bytes.NewReader(data)) // try to JPEG decode the new data
}

func loadFile(file string) (data []byte) {
	var err error
	data, err = ioutil.ReadFile(file)
	if err != nil {
		panic(err)
		return
	}
	//data = data[:len(data)-36] // remove the last 36 bytes appended by the ransomware

	// find the last SOS marker (there could be one for thumbnail)
	id := bytes.LastIndex(data, []byte{0xFF, 0xDA})
	if id == -1 || id < 10240 { // if no marker is found, or the marker is withing the encrypted 10kb
		var padding int
		fmt.Printf(">> No SOS (0xFFDA) marker found. first 10kb will be striped.\n>> How much padding do you want in the begining of the stream? ")
		fmt.Scanln(&padding)

		// extract all but the encrypted 10kb
		data = data[10240:]
		if padding > 0 {
			// prepend padding
			data = append(bytes.Repeat([]byte{0}, padding), data...)
		}
	} else {
		// extract SOS bloc
		data = data[id:]
	}
	return
}

func loadModels() {
	selfPath, err := os.Executable()
	if err != nil {
		panic(err)
	}

	filepath.Walk(filepath.Join(filepath.Dir(selfPath), modelDir), func(path string, info os.FileInfo, err error) error {
		if info == nil || err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		model, err := modelLoad(path)
		if err == nil {
			gModels = append(gModels, model)
		} else {
			fmt.Printf("Model load error: %v\n", err)
		}
		return nil
	})

	if len(gModels) <= 0 {
		panic(fmt.Sprintf("No models found!\nCreate a folder called \"__models__\" aside this executable and fill it with pictures taken with the same cameras as the encrypted ones, with different resolutions, orientation and quality settings. Rename the pictures because the model name will be appended to the recovered file (ex. sony-1080p-paysage.jpg"))
	}
}

func modelLoad(file string) (m tModel, err error) {
	var data []byte
	data, err = ioutil.ReadFile(file)
	if err != nil {
		return
	}

	id := bytes.LastIndex(data, []byte{0xFF, 0xDA})
	if id == -1 {
		panic(fmt.Sprintf("Model %s is invalid JPEG (no SOS 0xFFDA marker)", filepath.Base(file)))
	}

	m.name = filepath.Base(file)
	m.header = data[:id]

	var sz uint16
	binary.Read(bytes.NewReader(data[id+2:id+4]), binary.BigEndian, &sz)
	m.sosBloc = data[id : id+2+int(sz)]

	return
}

// askForConfirmation asks the user for confirmation. A user must type in "yes" or "no" and
// then press enter. It has fuzzy matching, so "y", "Y", "yes", "YES", and "Yes" all count as
// confirmations. If the input is not recognized, it will ask again. The function does not return
// until it gets a valid response from the user.
//
// https://gist.github.com/m4ng0squ4sh/3dcbb0c8f6cfe9c66ab8008f55f8f28b
func askForConfirmation(s string) bool {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Printf("%s [y/n]: ", s)

		response, err := reader.ReadString('\n')
		if err != nil {
			panic(err)
		}

		response = strings.ToLower(strings.TrimSpace(response))

		if response == "y" || response == "yes" {
			return true
		} else if response == "n" || response == "no" {
			return false
		}
	}
}
