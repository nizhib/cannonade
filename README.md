# Cannonade

Cannonade is your favorite tool for cannonading Web API services<sup><a>[1](#f1)</a></sup>.

<sup id="f1">1</sup> At least for those ones that support JSON inputs
with an `image` field containing a base64-encoded JPEG image.

## Installation
```bash
go get -u github.com/nizhib/cannonade
```

## Usage
```
Usage: cannonade [options...] <url>

Options:
  -apikey        API Key to use as a query parameter. Default is empty.
  -image         Path of the image to shoot with. Default is "example.jpg".
  -num-clients   Number of parallel requests. Default is 8.
  -num-requests  Total number of requests. Default is 100.
  -timeout       Request timeout limit. Default is 10.0.
  -silent        Disable any output but errors.
  -verbose       Print every response received.
```
