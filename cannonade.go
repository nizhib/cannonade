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
	"github.com/montanaflynn/stats"
	"github.com/schollz/progressbar"
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

type Response struct {
	Body string
	Latency time.Duration
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

func fire(endpoint string, ball []byte, apikey string) string {
	client := http.Client{
		Timeout: timeout,
	}
	buf := bytes.NewBuffer(ball)

	url := endpoint
	if apikey != "" {
		url += "?apikey=" + apikey
	}

	res, err := client.Post(url, "application/json; charset=utf-8", buf)
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

func cannonade(endpoint string, apikey string, pipeline <-chan []byte, responses chan<- Response) {
	for cannonball := range pipeline {
		start := time.Now()
		body := fire(endpoint, cannonball, apikey)
		latency := time.Since(start)
		responses <- Response{body, latency}
	}
}

func printStats(latencies []float64) {
	min, err := stats.Min(latencies)
	panicIf(err)
	median, err := stats.Median(latencies)
	panicIf(err)
	max, err := stats.Max(latencies)
	panicIf(err)
	sum, err := stats.Sum(latencies)
	panicIf(err)

	avg := sum / float64(len(latencies))
	rps := 1000 / avg

	fmt.Println()
	fmt.Println(" # reqs     Avg     Min     Max  |  Median   req/s  ")
	fmt.Println("----------------------------------------------------")
	fmt.Printf("%7d", len(latencies))
	fmt.Printf("%8.0f", avg)
	fmt.Printf("%8.0f", min)
	fmt.Printf("%8.0f", max)
	fmt.Print("  |")
	fmt.Printf("%8.0f", median)
	fmt.Printf("%8.2f\n", rps)

	fmt.Println()

	pthresholds := []int64{50, 80, 90, 95, 99, 100}
	percentiles := make([]float64, len(pthresholds))

	for i, threshold := range pthresholds {
		percentiles[i], err = stats.Percentile(latencies, float64(threshold))
		if err != nil {
			percentiles[i] = math.NaN()
		}
	}

	fmt.Println(" # reqs     50%    80%    90%    95%    99%   100%  ")
	fmt.Println("----------------------------------------------------")
	fmt.Printf("%7d ", len(latencies))
	for _, percentile := range percentiles {
		fmt.Printf("%7.0f", percentile)
	}
	fmt.Print("\n")
}

func main() {
	// Parse CLI options
	imagePath := flag.String("image", defaultImage, "path of the image to shoot with")
	numClients := flag.Int("num-clients", defaultNumClients, "number of parallel requests")
	numRequests := flag.Int("num-requests", defaultNumRequests, "total number of requests")
	apikey := flag.String("apikey", "", "api key to add in the header")
	verbose := flag.Bool("verbose", false, "Show each response in stdout")
	silent := flag.Bool("silent", false, "Disable any output")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		fmt.Println("Provide an endpoint to shoot at!")
		os.Exit(1)
	}
	endpoint := args[0]

	// Open an image to shoot with
	img, err := readImage(*imagePath)
	if err != nil {
		fmt.Printf("Failed opening file as jpeg image: %s", err)
		os.Exit(1)
	}

	// Create channels
	pipeline := make(chan []byte, *numRequests)
	responses := make(chan Response, *numRequests)
	latencies := make([]float64, *numRequests)

	// Prepare binary requests bodies
	if !*silent {
		fmt.Print("Producing cannonballs... ")
	}
	for r := 0; r < *numRequests; r++ {
		pipeline <- makeCannonball(img)
	}
	if !*silent {
		fmt.Print("done")
		if *verbose {
			fmt.Print("\n")
		}
	}

	// Fire parallel web requests
	for c := 0; c < *numClients; c++ {
		go cannonade(endpoint, *apikey, pipeline, responses)
	}

	// Gather stats from responses
	var bar *progressbar.ProgressBar
	if !*silent && !*verbose {
		bar = progressbar.New(*numRequests)
		fmt.Print("\r")
	}
	for r := 0; r < *numRequests; r++ {
		response := <-responses
		latencies[r] = float64(response.Latency) / math.Pow10(6)
		if !*silent && *verbose {
			_, err := fmt.Println(response.Body)
			panicIf(err)
		}
		if bar != nil {
			err = bar.Add(1)
			panicIf(err)
		}
	}
	if !*silent && !*verbose {
		fmt.Println()
	}

	// Print pretty stats table
	if !*silent {
		printStats(latencies)
	}
}
