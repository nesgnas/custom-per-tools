package main

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/opts"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var urls = []string{
	"https://green-apis.nesgnas.uk/persons",
	"https://apis.nesgnas.uk/persons",
}

const repeat = 30
const outDir = "hey_results"
const requestCounter = 1000
const worker = 100

type HeyResult struct {
	URL     string
	File    string
	RPS     float64
	P95     float64
	Average float64
	Total   float64
}

func readCSV(path string) ([]HeyResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	headers, _ := reader.Read()
	index := make(map[string]int)
	for i, h := range headers {
		index[h] = i
	}

	records, _ := reader.ReadAll()
	var results []HeyResult

	for _, row := range records {
		r := HeyResult{
			File:    row[index["file"]],
			URL:     inferURLFromFile(row[index["file"]]),
			RPS:     parseFloat(row[index["requests_per_sec"]]),
			P95:     parseFloat(row[index["p95"]]),
			Average: parseFloat(row[index["average"]]),
			Total:   parseFloat(row[index["total"]]),
		}
		results = append(results, r)
	}
	return results, nil
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func inferURLFromFile(filename string) string {
	if strings.Contains(filename, "green") {
		return "green-cloud"
	}
	return "t2no3"
}

func generateLineChart(data []HeyResult, metric string, title string, filename string) {
	line := charts.NewLine()
	line.SetGlobalOptions(
		charts.WithTitleOpts(opts.Title{Title: title}),
		charts.WithYAxisOpts(opts.YAxis{Name: metric}),
		charts.WithXAxisOpts(opts.XAxis{Name: "Test Run"}),
	)

	urlGroups := map[string][]opts.LineData{}
	xAxis := []string{}
	for i := 1; i <= repeat; i++ {
		xAxis = append(xAxis, fmt.Sprintf("%d", i))
	}

	for _, d := range data {
		urlGroups[d.URL] = append(urlGroups[d.URL], opts.LineData{Value: extractMetric(d, metric)})
	}

	line.SetXAxis(xAxis)
	for url, series := range urlGroups {
		line.AddSeries(url, series)
	}

	f, _ := os.Create(filename)
	defer f.Close()
	line.Render(f)
	fmt.Printf("✅ Chart written to %s\n", filename)
}

func extractMetric(r HeyResult, metric string) float64 {
	switch metric {
	case "rps":
		return r.RPS
	case "p95":
		return r.P95
	case "average":
		return r.Average
	case "total":
		return r.Total
	default:
		return 0
	}
}

func slugifyURL(url string) string {
	// Replace https:// and all non-alphanum with _
	slug := strings.ReplaceAll(url, "https://", "")
	return regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(slug, "_")
}

func runHey(url string, i int) (string, error) {
	slug := slugifyURL(url)
	outFile := filepath.Join(outDir, fmt.Sprintf("hey_result_%s_%d.txt", slug, i))

	cmd := exec.Command("hey", "-n", strconv.Itoa(requestCounter), "-c", strconv.Itoa(worker), "-m", "GET", url)
	outBytes, err := cmd.Output()
	if err != nil {
		return "", err
	}

	os.WriteFile(outFile, outBytes, 0644)
	return outFile, nil
}

func extractFloat(re *regexp.Regexp, line string) float64 {
	match := re.FindStringSubmatch(line)
	if len(match) >= 2 {
		val, _ := strconv.ParseFloat(match[1], 64)
		return val
	}
	return 0
}

func parseHeyFile(file string) map[string]string {
	result := make(map[string]string)
	result["file"] = filepath.Base(file)

	f, _ := os.Open(file)
	defer f.Close()

	scanner := bufio.NewScanner(f)
	percentiles := map[string]*regexp.Regexp{
		"p50": regexp.MustCompile(`50% in ([\d.]+)`),
		"p75": regexp.MustCompile(`75% in ([\d.]+)`),
		"p90": regexp.MustCompile(`90% in ([\d.]+)`),
		"p95": regexp.MustCompile(`95% in ([\d.]+)`),
		"p99": regexp.MustCompile(`99% in ([\d.]+)`),
	}
	fields := map[string]*regexp.Regexp{
		"total":            regexp.MustCompile(`Total:\s+([\d.]+)`),
		"fastest":          regexp.MustCompile(`Fastest:\s+([\d.]+)`),
		"slowest":          regexp.MustCompile(`Slowest:\s+([\d.]+)`),
		"average":          regexp.MustCompile(`Average:\s+([\d.]+)`),
		"requests_per_sec": regexp.MustCompile(`Requests/sec:\s+([\d.]+)`),
		"size_request":     regexp.MustCompile(`Size/request:\s+([\d.]+)`),
	}

	for scanner.Scan() {
		line := scanner.Text()

		for k, re := range percentiles {
			if val := extractFloat(re, line); val != 0 {
				result[k] = fmt.Sprintf("%.4f", val)
			}
		}

		for k, re := range fields {
			if val := extractFloat(re, line); val != 0 {
				result[k] = fmt.Sprintf("%.4f", val)
			}
		}
	}

	return result
}

func writeCSV(data []map[string]string, filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	headers := []string{"file", "total", "average", "fastest", "slowest", "requests_per_sec", "size_request", "p50", "p75", "p90", "p95", "p99"}
	writer.Write(headers)

	for _, row := range data {
		record := make([]string, len(headers))
		for i, h := range headers {
			record[i] = row[h]
		}
		writer.Write(record)
	}

	return nil
}

func main() {
	os.RemoveAll(outDir)
	os.MkdirAll(outDir, 0755)

	var results []map[string]string

	for _, url := range urls {
		for i := 1; i <= repeat; i++ {
			fmt.Printf("→ Running test %d for %s\n", i, url)
			file, err := runHey(url, i)
			if err != nil {
				fmt.Printf("Error running hey: %v\n", err)
				continue
			}
			time.Sleep(1 * time.Second) // optional sleep between runs
			data := parseHeyFile(file)
			data["url"] = url
			results = append(results, data)
		}
	}

	err := writeCSV(results, "hey_results.csv")
	if err != nil {
		fmt.Println("❌ Error writing CSV:", err)
	} else {
		fmt.Println("✅ CSV written to hey_results.csv")
	}

	csvResults, err := readCSV("hey_results.csv")
	if err != nil {
		fmt.Println("Failed to read CSV:", err)
		return
	}

	generateLineChart(csvResults, "rps", "Requests Per Second", "chart_rps.html")
	generateLineChart(csvResults, "p95", "95th Percentile Latency", "chart_p95.html")
	generateLineChart(csvResults, "average", "Average Latency", "chart_avg.html")
	generateLineChart(csvResults, "total", "Total Time", "chart_total.html")

}
