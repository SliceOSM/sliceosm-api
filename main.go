package main

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
	"github.com/google/uuid"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/maptile"
	"github.com/paulmach/orb/maptile/tilecover"
	"github.com/paulmach/orb/planar"
	"image"
	"image/png"
	"io"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// used to estimate the nodes count on the client
// as well as enforce it on the server.
//
//go:embed z12_red_green.png
var imageBytes []byte

// global system state.
type SystemState struct {
	Status     string
	QueueSize  int
	NodesLimit int
	Timestamp  string
}

// the content of a POST request
type Input struct {
	Name       string
	RegionType string // geojson, bbox
	RegionData json.RawMessage
}

// A sanitized serialization of the submitted job
// used for the UI to display region, to re-create the region,
// and to name the download.
type Task struct {
	Uuid                string
	SanitizedName       string
	SanitizedRegionType string
	SanitizedRegionData json.RawMessage
}

// Used to display progress. When complete, is persisted
// on the filesystem but still served through /api endpoint.
type Progress struct {
	// fields that come from osmx
	Timestamp  string
	CellsTotal int64
	CellsProg  int64
	NodesTotal int64
	NodesProg  int64
	ElemsTotal int64
	ElemsProg  int64

	SizeBytes int64
	Elapsed   float64
	Complete  bool
}

type Server struct {
	progress      map[string]Progress
	progressMutex sync.RWMutex
	queue         chan Task
	filesDir      string
	tmpDir        string
	exec          string
	data          string
	image         image.Image
	nodesLimit    int

	lastUpdated LastUpdated
}

type LastUpdated struct {
	mutex     sync.Mutex
	timestamp time.Time
	checkedAt time.Time
}

func (h *Server) runTask(id int, task Task) error {
	uuid := task.Uuid
	fmt.Println("worker", id, "started job", uuid)
	start := time.Now()

	pbfPath := filepath.Join(h.tmpDir, uuid+".osm.pbf")

	regionPath := filepath.Join(h.tmpDir, uuid+"."+task.SanitizedRegionType)

	out, err := os.Create(regionPath)
	if err != nil {
		return err
	}
	if task.SanitizedRegionType == "bbox" {
		out.Write(task.SanitizedRegionData[1 : len(task.SanitizedRegionData)-1])
	} else {
		out.Write(task.SanitizedRegionData)
	}
	out.Close()

	taskJson, err := json.Marshal(task)
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(filepath.Join(h.filesDir, uuid+"_region.json"), taskJson, 0644)
	if err != nil {
		return err
	}

	args := []string{"extract", h.data, pbfPath, "--jsonOutput", "--region", regionPath}
	cmd := exec.Command(h.exec, args...)
	stdout, err := cmd.StdoutPipe()

	err = cmd.Start()
	if err != nil {
		return err
	}
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	for err == nil {
		var progress Progress
		if err := json.NewDecoder(strings.NewReader(line)).Decode(&progress); err != nil {
			return err
		}
		h.progressMutex.Lock()
		h.progress[uuid] = progress
		h.progressMutex.Unlock()
		line, err = reader.ReadString('\n')
	}
	err = cmd.Wait()
	if err != nil {
		return err
	}

	f, err := os.Open(pbfPath)
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}

	if err := os.Rename(pbfPath, filepath.Join(h.filesDir, uuid+".osm.pbf")); err != nil {
		return err
	}

	if err := os.Remove(regionPath); err != nil {
		return err
	}

	var lastProgress Progress
	h.progressMutex.Lock()
	lastProgress = h.progress[uuid]
	delete(h.progress, uuid)
	h.progressMutex.Unlock()

	elapsed := time.Since(start).Seconds()
	lastProgress.Elapsed = elapsed
	lastProgress.Complete = true
	lastProgress.SizeBytes = stat.Size()
	completion, err := json.Marshal(lastProgress)
	if err != nil {
		return err
	}
	if err := ioutil.WriteFile(filepath.Join(h.filesDir, uuid), completion, 0644); err != nil {
		return err
	}
	fmt.Println("worker", id, "finished job", uuid, "in", elapsed)
	return nil
}

func (h *Server) worker(id int, queue chan Task) {
	for task := range queue {
		h.progressMutex.Lock()
		h.progress[task.Uuid] = Progress{}
		h.progressMutex.Unlock()

		err := h.runTask(id, task)
		if err != nil {
			fmt.Println(err)
			sentry.CaptureException(err)
			sentry.Flush(time.Second * 5)
		}
	}
}

func (h *Server) StartWorkers() {
	h.queue = make(chan Task, 512)
	h.progress = make(map[string]Progress)

	for i := 0; i < runtime.NumCPU(); i++ {
		go h.worker(i, h.queue)
	}
}

func GetSum(image image.Image, geom orb.Geometry) int {
	var covering map[maptile.Tile]bool
	for z := 0; z <= 14; z++ {
		covering, _ = tilecover.Geometry(geom, maptile.Zoom(z))
		if len(covering) > 256 {
			break
		}
	}

	sum := 0.0
	for t := range covering {
		sum += GetPixel(image, int(t.Z), int(t.X), int(t.Y))
	}

	return int(sum * 32)
}

func GetPixel(image image.Image, z int, x int, y int) float64 {
	if z < 12 {
		dz := 2 << ((12 - z) - 1)
		acc := 0.0
		for ix := x * dz; ix < x*dz+dz; ix++ {
			for iy := y * dz; iy < y*dz+dz; iy++ {
				color := image.At(int(ix), int(iy))
				red, green, _, _ := color.RGBA()
				acc += float64(int(red>>8)*256) + float64(green>>8)
			}
		}
		return acc
	} else if z == 12 {
		red, green, _, _ := image.At(x, y).RGBA()
		return float64(int(red>>8)*256) + float64(green>>8)
	} else {
		dz := 2 << ((z - 12) - 1)
		x := int(math.Floor(float64(x) / float64(dz)))
		y := int(math.Floor(float64(y) / float64(dz)))
		red, green, _, _ := image.At(x, y).RGBA()
		return float64(int(red>>8)*256+int(green>>8)) / float64(dz*dz)
	}
}

func parseInput(body io.Reader) (orb.Geometry, string, string, json.RawMessage, error) {
	decoder := json.NewDecoder(body)

	var input Input
	err := decoder.Decode(&input)
	if err != nil {
		return nil, "", "", nil, errors.New("input GeoJSON is invalid")
	}

	var geom orb.Geometry
	var sanitizedData json.RawMessage

	if input.RegionType == "geojson" {
		geojsonGeom, err := geojson.UnmarshalGeometry(input.RegionData)
		if err != nil {
			return nil, "", "", nil, errors.New("input GeoJSON is invalid")
		}
		geom = geojsonGeom.Geometry()
		switch v := geom.(type) {
		case orb.Polygon:
			if len(v) == 0 {
				return nil, "", "", nil, errors.New("geom does not have enough rings")
			}
			for _, ring := range v {
				if len(ring) < 4 {
					return nil, "", "", nil, errors.New("ring does not have enough coordinates")
				}
			}
		case orb.MultiPolygon:
			if len(v) == 0 {
				return nil, "", "", nil, errors.New("geom does not have enough rings")
			}
			for _, polygon := range v {
				if len(polygon) == 0 {
					return nil, "", "", nil, errors.New("geom does not have enough rings")
				}
				for _, ring := range polygon {
					if len(ring) < 4 {
						return nil, "", "", nil, errors.New("ring does not have enough coordinates")
					}
				}
			}
		}
		sanitizedData, _ = geojsonGeom.MarshalJSON()
	} else if input.RegionType == "bbox" {
		var coords []float64
		json.Unmarshal(input.RegionData, &coords)
		if len(coords) < 4 {
			return nil, "", "", nil, errors.New("input does not have >3 coordinates")
		}
		geom = orb.MultiPoint{orb.Point{coords[1], coords[0]}, orb.Point{coords[3], coords[2]}}.Bound()
		sanitizedData, _ = json.Marshal(coords[0:4])
	} else {
		return nil, "", "", nil, errors.New("invalid input RegionType")
	}

	if planar.Area(geom) == 0.0 {
		return nil, "", "", nil, errors.New("Input has 0 area")
	}

	return geom, input.Name, input.RegionType, sanitizedData, nil
}

// check the filesystem for the result JSON
// if it's not started yet, return the position in the queue
func (h *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == "POST" {
		geom, sanitized_name, sanitized_type, sanitized_region, err := parseInput(r.Body)

		if err != nil {
			w.WriteHeader(400)
			return
		}

		if GetSum(h.image, geom) > h.nodesLimit {
			w.WriteHeader(400)
			fmt.Fprintf(w, "Error: the limit of nodes was exceeded.")
			return
		}

		task := Task{Uuid: uuid.New().String(), SanitizedName: sanitized_name, SanitizedRegionType: sanitized_type, SanitizedRegionData: sanitized_region}

		select {
		case h.queue <- task:
			var progress Progress
			h.progressMutex.Lock()
			h.progress[task.Uuid] = progress
			h.progressMutex.Unlock()
			w.WriteHeader(201)
			fmt.Fprintf(w, task.Uuid)
		default:
			w.WriteHeader(503)
		}
	} else {
		if r.URL.Path == "/api" || r.URL.Path == "/api/" {
			l := len(h.queue)

			h.lastUpdated.mutex.Lock()

			if time.Since(h.lastUpdated.checkedAt).Seconds() > 10 {
				cmd := exec.Command(h.exec, "query", h.data, "timestamp")
				timestampRaw, _ := cmd.Output()
				timestamp, err := time.Parse(time.RFC3339, strings.TrimSpace(string(timestampRaw)))
				if err == nil {
					h.lastUpdated.timestamp = timestamp
					h.lastUpdated.checkedAt = time.Now()
				}
			}

			timestamp := h.lastUpdated.timestamp
			h.lastUpdated.mutex.Unlock()

			w.Header().Set("Content-Type", "application/json")

			status := "ok"
			if time.Now().Sub(timestamp).Minutes() > 15 {
				status = "warn"
			}

			json.NewEncoder(w).Encode(SystemState{status, l, h.nodesLimit, timestamp.Format(time.RFC3339)})
		} else if r.URL.Path == "/api/nodes.png" {
			w.Header().Set("Content-Type", "image/png")
			w.Write(imageBytes)
		} else {
			parts := strings.Split(r.URL.Path, "/")
			if len(parts) != 3 || parts[0] != "" || parts[1] != "api" {
				w.WriteHeader(404)
				return
			}
			uuid := parts[2]

			h.progressMutex.RLock()
			progress, ok := h.progress[uuid]
			h.progressMutex.RUnlock()

			if ok {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(progress)
				return
			}

			resultPath := filepath.Join(h.filesDir, uuid)
			if _, err := os.Stat(resultPath); err == nil {
				Openfile, _ := os.Open(resultPath)
				defer Openfile.Close()
				w.Header().Set("Content-Type", "application/json")
				io.Copy(w, Openfile)
				return
			}
			w.WriteHeader(404)
		}
	}
}

func main() {
	var (
		bindAddress, filesDir, exec, sentryDsn string
	)
	var nodesLimit int
	flag.StringVar(&bindAddress, "bind", ":8080", "IP address and port to listen on")
	flag.StringVar(&filesDir, "filesDir", "", "Result directory")
	flag.StringVar(&exec, "exec", "osmx", "Path to OSMX executable")
	flag.StringVar(&sentryDsn, "sentryDsn", "", "Sentry DSN")
	flag.IntVar(&nodesLimit, "nodesLimit", 100000000, "Nodes limit")

	flag.Usage = func() {
		fmt.Printf("SliceOSM API server\n\n")
		fmt.Printf("Usage: %s [OPTIONS] OSMX_FILE\n\n", os.Args[0])
		fmt.Println("Options:")
		flag.PrintDefaults()
	}

	flag.Parse()

	if filesDir == "" {
		fmt.Println("Error: missing required option -filesDir")
		flag.Usage()
		os.Exit(2)
	}

	tmpDir := os.Getenv("TMPDIR")
	if tmpDir == "" {
		tmpDir = "/tmp"
	}

	if flag.NArg() != 1 {
		fmt.Println("Error: missing required argument OSMX_FILE")
		flag.Usage()
		os.Exit(2)
	}

	data := flag.Arg(0)

	if sentryDsn != "" {
		err := sentry.Init(sentry.ClientOptions{
			Dsn: sentryDsn,
		})

		if err != nil {
			fmt.Printf("Sentry initialization failed: %v\n", err)
		}
	}

	img, err := png.Decode(bytes.NewReader(imageBytes))
	if err != nil {
		fmt.Println("Error decoding file:", err)
		return
	}

	srv := Server{
		filesDir:   filesDir,
		tmpDir:     tmpDir,
		exec:       exec,
		data:       data,
		image:      img,
		nodesLimit: nodesLimit,
	}
	srv.StartWorkers()
	fmt.Printf("Starting server on %s\n", bindAddress)
	sentryHandler := sentryhttp.New(sentryhttp.Options{})
	http.Handle("/", sentryHandler.Handle(&srv))
	log.Fatal(http.ListenAndServe(bindAddress, nil))
}
