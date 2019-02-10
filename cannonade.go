// Copyright (c) 2019 Evgeny Nizhibitsky
//
// Permission is hereby granted, free of charge, to any person obtaining a
// copy of this software and associated documentation files (the "Software"),
// to deal in the Software without restriction, including without limitation
// the rights to use, copy, modify, merge, publish, distribute, sublicense,
// and/or sell copies of the Software, and to permit persons to whom the
// Software is furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS
// OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING
// FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER
// DEALINGS IN THE SOFTWARE.

// Cannonade is your favorite tool for cannonading Web API services
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
	"math/rand"
	"net/http"
	"os"
	"time"
)

const defaultImage = "example.jpg"
const defaultNumClients = 8
const defaultNumRequests = 100

const noiseIterations = 100
const jpegQuality = 95
const timeout = 60 * time.Second

type Request struct {
	Image string `json:"image"`
}

func panicIf(err error) {
	if err != nil {
		panic(err)
	}
}

func readImage(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	img, err := jpeg.Decode(file)
	if err != nil {
		return nil, err
	}

	return img, nil
}

func addNoise(img *image.Image) image.Image {
	src := rand.NewSource(time.Now().UnixNano())
	rnd := rand.New(src)

	bounds := (*img).Bounds()
	noisy := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(noisy, noisy.Bounds(), *img, bounds.Min, draw.Src)

	for i := 0; i < noiseIterations; i++ {
		x := rnd.Intn(bounds.Max.X)
		y := rnd.Intn(bounds.Max.Y)
		val := uint8(rnd.Intn(math.MaxUint8))
		noisy.Set(x, y, color.RGBA{R: val, G: val, B: val, A: math.MaxUint8})
	}

	return noisy
}

func encodeImage(img *image.Image) string {
	buf := bytes.NewBuffer(make([]byte, 0))

	err := jpeg.Encode(buf, *img, &jpeg.Options{Quality: jpegQuality})
	panicIf(err)

	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())

	return encoded
}

func makeCannonball(img image.Image) []byte {
	noisy := addNoise(&img)

	encoded := encodeImage(&noisy)

	req := Request{encoded}
	cannonball, err := json.Marshal(&req)
	panicIf(err)

	return cannonball
}

func fire(endpoint string, ball []byte) string {
	client := http.Client{
		Timeout: timeout,
	}
	buf := bytes.NewBuffer(ball)
	res, err := client.Post(endpoint, "application/json; charset=utf-8", buf)
	if err != nil {
		return fmt.Sprintf("Error while sending the request: %s", err)
	}

	buf = new(bytes.Buffer)
	_, err = buf.ReadFrom(res.Body)
	if err != nil {
		return fmt.Sprintf("Error while parsing the response: %s", err)
	}

	return buf.String()
}

func cannonade(endpoint string, pipeline <-chan []byte, responses chan<- string) {
	for cannonball := range pipeline {
		responses <- fire(endpoint, cannonball)
	}
}

func main() {
	imagePath := flag.String("image", defaultImage, "path of the image to shoot with")
	numClients := flag.Int("num-clients", defaultNumClients, "number of parallel requests")
	numRequests := flag.Int("num-requests", defaultNumRequests, "total number of requests")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Println("Provide an endpoint to shoot at!")
		os.Exit(1)
	}
	endpoint := args[0]

	img, err := readImage(*imagePath)
	if err != nil {
		fmt.Printf("Failed opening file as jpeg image: %s", err)
		os.Exit(1)
	}

	pipeline := make(chan []byte, *numRequests)
	responses := make(chan string, *numRequests)

	fmt.Print("Producing cannonballs... ")
	for r := 0; r < *numRequests; r++ {
		pipeline <- makeCannonball(img)
	}
	close(pipeline)
	fmt.Print("done\n")

	for c := 0; c < *numClients; c++ {
		go cannonade(endpoint, pipeline, responses)
	}

	for r := 0; r < *numRequests; r++ {
		_, err := fmt.Println(<-responses)
		panicIf(err)
	}
}
