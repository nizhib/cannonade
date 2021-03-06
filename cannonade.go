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
	"github.com/schollz/progressbar/v2"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/montanaflynn/stats"
)

const defaultImage = "example.jpg"
const defaultSchedule = ""
const defaultNumClients = 8
const defaultNumRequests = 100
const defaultTimeout = 10.0

const noiseIterations = 100
const jpegQuality = 95

// Request : A simple API request object with base64-encoded JPEG image
type Request struct {
	Image string `json:"image"`
}

// Response : Body from the API response as well as additional info
type Response struct {
	Body    string
	Success bool
	Latency time.Duration
}

// Task : A load pattern to execute
type Task struct {
	Endpoint    string
	Image       image.Image
	Noisy       bool
	NumRequests int
	NumClients  int
}

// Options: task execution options
type Options struct {
	Timeout  float64
	ApiKey   string
	Silent   bool
	Verbose  bool
	Metrics  bool
	Progress bool
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

func makeCannonball(img image.Image, noisy bool) []byte {
	if noisy {
		img = addNoise(&img)
	}

	encoded := encodeImage(&img)

	req := Request{encoded}
	cannonball, err := json.Marshal(&req)
	panicIf(err)

	return cannonball
}

func fire(endpoint string, ball []byte, timeout float64, apikey string) (string, bool) {
	client := http.Client{
		Timeout: time.Duration(timeout * float64(time.Second)),
	}
	buf := bytes.NewBuffer(ball)

	url := endpoint
	if apikey != "" {
		url += "?apikey=" + apikey
	}

	res, err := client.Post(url, "application/json; charset=utf-8", buf)
	if err != nil {
		return fmt.Sprintf("Error while sending the request: %s", err), false
	}

	buf = new(bytes.Buffer)
	_, err = buf.ReadFrom(res.Body)
	if err != nil {
		return fmt.Sprintf("Error while parsing the response: %s", err), false
	}

	return buf.String(), res.StatusCode == 200
}

func cannonade(endpoint string, timeout float64, apikey string,
	pipeline <-chan []byte, responses chan<- Response, metrics bool) {

	var logger *log.Logger
	if metrics {
		f, err := os.OpenFile("metrics.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		panicIf(err)
		logger = log.New(f, "", 0)
	}

	for cannonball := range pipeline {
		start := time.Now()
		body, success := fire(endpoint, cannonball, timeout, apikey)
		latency := time.Since(start)
		if logger != nil {
			panicIf(logger.Output(2, fmt.Sprintf("%3.3f", float64(latency)/math.Pow10(6))))
		}
		responses <- Response{body, success, latency}
	}
}

func printStats(latencies []float64, totalSeconds float64, numRequests int, numFails int) {
	min, err := stats.Min(latencies)
	if err != nil {
		min = math.NaN()
	}
	median, err := stats.Median(latencies)
	if err != nil {
		median = math.NaN()
	}
	max, err := stats.Max(latencies)
	if err != nil {
		max = math.NaN()
	}
	sum, err := stats.Sum(latencies)
	if err != nil {
		sum = math.NaN()
	}

	avg := sum / float64(numRequests)
	rps := float64(numRequests) / totalSeconds

	fmt.Println(" # reqs   # fails     Avg     Min     Max  |  Median   req/s  ")
	fmt.Println("--------------------------------------------------------------")
	fmt.Printf("%7d", numRequests)
	fmt.Printf("%10d", numFails)
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
	fmt.Printf("%7d ", numRequests)
	for _, percentile := range percentiles {
		fmt.Printf("%7.0f", percentile)
	}
	fmt.Print("\n")
}

func runTask(task *Task, opt *Options) {
	// Create channels
	pipeline := make(chan []byte, task.NumRequests)
	responses := make(chan Response, task.NumRequests)

	// Prepare binary requests bodies
	if !opt.Silent && opt.Verbose && task.NumRequests > 1 {
		fmt.Print("Producing cannonballs... ")
	}
	cannonball := makeCannonball(task.Image, task.Noisy)
	for r := 0; r < task.NumRequests; r++ {
		if task.Noisy && r > 0 {
			cannonball = makeCannonball(task.Image, task.Noisy)
		}
		pipeline <- cannonball
	}
	if !opt.Silent && opt.Verbose && task.NumRequests > 1 {
		fmt.Print("done\n")
	}

	// Fire parallel web requests
	start := time.Now()
	for c := 0; c < task.NumClients; c++ {
		go cannonade(task.Endpoint, opt.Timeout, opt.ApiKey, pipeline, responses, opt.Metrics)
	}

	// Gather stats from responses
	var bar *progressbar.ProgressBar
	if !opt.Silent && opt.Progress {
		bar = progressbar.New(task.NumRequests)
		err := bar.RenderBlank()
		panicIf(err)
		fmt.Print("\r")
	}
	var latencies = make([]float64, 0)
	var numFails = 0
	for r := 0; r < task.NumRequests; r++ {
		response := <-responses
		if response.Success {
			latencies = append(latencies, float64(response.Latency)/math.Pow10(6))
		} else {
			numFails++
		}
		if !opt.Silent && opt.Verbose {
			_, err := fmt.Println(response.Body)
			panicIf(err)
		}
		if bar != nil {
			err := bar.Add(1)
			panicIf(err)
		}
	}
	if !opt.Silent && opt.Progress {
		fmt.Println()
	}
	totalSeconds := float64(time.Since(start)) / math.Pow10(9)

	// Print pretty stats table
	if !opt.Silent {
		fmt.Printf("\nTask: %d@%d\n\n", task.NumRequests, task.NumClients)
		printStats(latencies, totalSeconds, task.NumRequests, numFails)
	}
}

func main() {
	// Parse CLI options
	imagePath := flag.String("image", defaultImage, "path of the image to shoot with")
	schedule := flag.String("schedule", defaultSchedule, "requests load schedule (5@1,10@2)")
	numRequests := flag.Int("num-requests", defaultNumRequests, "total number of requests")
	numClients := flag.Int("num-clients", defaultNumClients, "number of parallel requests")
	noisy := flag.Bool("noisy", false, "add random noise to each request")
	timeout := flag.Float64("timeout", defaultTimeout, "request timeout limit")
	apikey := flag.String("apikey", "", "api key to use as a query parameter")
	verbose := flag.Bool("verbose", false, "print every response to stdout")
	metrics := flag.Bool("metrics", false, "save latencies to metrics.log file")
	progress := flag.Bool("progress", false, "show progressbar")
	silent := flag.Bool("silent", false, "disable any output but errors")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		fmt.Println("Provide an endpoint to shoot at!")
		os.Exit(1)
	}
	endpoint := args[0]

	// Check options compatibility
	if *progress && *verbose {
		fmt.Println("Cannot use progress and verbose flags together")
		os.Exit(1)
	}

	// Open an image to shoot with
	img, err := readImage(*imagePath)
	if err != nil {
		fmt.Printf("Failed reading the image: %s\n", err)
		os.Exit(1)
	}

	task := Task{
		Endpoint:    endpoint,
		Image:       img,
		Noisy:       *noisy,
		NumClients:  *numClients,
		NumRequests: *numRequests,
	}
	opt := Options{
		Silent:   *silent,
		Verbose:  *verbose,
		Metrics:  *metrics,
		Progress: *progress,
		Timeout:  *timeout,
		ApiKey:   *apikey,
	}

	if *schedule == "" {
		*schedule = fmt.Sprintf("%d@%d", *numRequests, *numClients)
	}

	for _, milestone := range strings.Split(*schedule, ",") {
		numRequests, err := strconv.Atoi(strings.Split(milestone, "@")[0])
		panicIf(err)
		task.NumRequests = numRequests

		numClients, err := strconv.Atoi(strings.Split(milestone, "@")[1])
		panicIf(err)
		task.NumClients = numClients

		runTask(&task, &opt)
	}
}
