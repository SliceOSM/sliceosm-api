# sliceosm-api

A lightweight web server for submitting extract tasks to an [OSM Express](http://github.com/bdon/OSMExpress) database.

## Usage

```
Usage: ./sliceosm-api [OPTIONS] OSMX_FILE

Options:
  -bind string
        IP address and port to listen on
  -exec string
        Path to OSMX executable
  -filesDir string
        Result directory
  -nodesLimit int
        Nodes limit (default 100000000)
  -sentryDsn string
        Sentry DSN
```

## API

### Quickstart example

```sh
curl -X POST https://slice.openstreetmap.us/api/ -d '{"Name":"none","RegionType":"geojson","RegionData":{"type":"Polygon","coordinates":[[[-77.4571,37.5530],[-77.4571,37.5272],[-77.4133,37.5272],[-77.4133,37.5530],[-77.4571,37.5530]]]}}'
# 2637da98-20a1-428f-b6db-18ac2861b763
curl https://slice.openstreetmap.us/api/2637da98-20a1-428f-b6db-18ac2861b763
# when Complete is true, fetch the file:
curl https://slice.openstreetmap.us/files/2637da98-20a1-428f-b6db-18ac2861b763.osm.pbf -o out.osm.pbf
```

### GET `/`

Returns:

- the last updated timestamp
- the nodes limit for the server
- the number of jobs in the queue

### GET `/nodes.png`

Returns an PNG-encoded representation of OSM node density.

### POST `/`

Create a task.

Examples of creating tasks: (your `.osmx` database must include Richmond, Virginia)

```
curl -X POST http://localhost:8080 -d '{"Name":"none", "RegionType":"bbox", "RegionData":[37.5272,-77.4571,37.5530,-77.4133]}'

curl -X POST http://localhost:8080 -d '{"Name":"none","RegionType":"geojson","RegionData":{"type":"Polygon","coordinates":[[[-77.4571,37.5530],[-77.4571,37.5272],[-77.4133,37.5272],[-77.4133,37.5530],[-77.4571,37.5530]]]}}'
```

- `RegionType` - one of `bbox`, `geojson`

`bbox`: in `min_lat,min_lon,max_lat,max_lon` format

`geojson`: a GeoJSON Geometry, either a Polygon or MultiPolygon 

* up to the configured nodes limit of the server.
* Limit on the number of vertices in the input polygon.

Returns a UUID or an error message.

### GET `/{uuid}`

Get a JSON Progress for a task submitted in the last 24 hours.

```js
{
  "Timestamp": 
  "CellsProg": "",
  "CellsTotal": "",
  "NodesProg": "",
  "NodesTotal": "",
  "ElemsProg": "",
  "ElemsTotal":"",
  "SizeBytes":"",
  "Elapsed":"",
  "Complete":""
}
```

## File Server

These paths are not served through the API, but by a static fileserver.

### GET `/{uuid}_region.json`

Get the GeoJSON submitted for this task. Valid immediately after the task is accepted by the server.

```json
{
  "Uuid":"",
  "SanitizedName": "abcd",
  "SanitizedRegionType":"",
  "SanitizedRegionData":""
}
```

### GET `/{uuid}.osm.pbf`

Download the result `osm.pbf`. This appears once the Get `/{uuid}` API reports `Completed`.

## Building

Cross-compile the `sliceosm-api` ARM linux binary:

```
GOOS=linux GOARCH=arm64 go build
```

Example crontab for updating an osmx database and cleaning up results older than one day:

```
PATH=/home/osmx/OSMExpress:$PATH
* * * * * /bin/sleep 8 && /usr/bin/python3 /home/osmx/OSMExpress/utils/osmx-update /mnt/planet.osmx https://planet.openstreetmap.org/replication/minute/ >> /home/osmx/osmx-update.log 2>&1
0 0 * * * find /mnt/www/files/ -type f -mtime +1 -exec rm {} \;
```
