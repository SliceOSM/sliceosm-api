package main

import (
	"github.com/stretchr/testify/assert"
	"image/png"
	"os"
	"strings"
	"testing"
)

func TestGetPixel(t *testing.T) {
	file, _ := os.Open("z12_red_green.png")
	defer file.Close()
	img, _ := png.Decode(file)
	assert.Equal(t, 249.125, GetPixel(img, 14, 2620, 6331))
}

func TestGeoJSON(t *testing.T) {
	_, name, regiontype, _, err := parseInput(strings.NewReader(`{"Name":"a_name", "RegionType":"geojson", "RegionData":{"type":"Polygon","coordinates":[[[0,0],[1,1],[1,0],[0,0]]]}}`))
	assert.Nil(t, err)
	assert.Equal(t, "a_name", name)
	assert.Equal(t, "geojson", regiontype)
}

func TestBbox(t *testing.T) {
	_, name, regiontype, _, err := parseInput(strings.NewReader(`{"Name":"a_name", "RegionType":"bbox", "RegionData":[0,0,1,1]}`))
	assert.Nil(t, err)
	assert.Equal(t, "a_name", name)
	assert.Equal(t, "bbox", regiontype)
}

func TestInvalidGeoJSON(t *testing.T) {
	_, _, _, _, err := parseInput(strings.NewReader(`{"Name":"a_name", "RegionType":"geojson", "RegionData":null}`))
	assert.NotNil(t, err)
}

func TestMalformedGeoJSON(t *testing.T) {
	_, _, _, _, err := parseInput(strings.NewReader(`{"Name":"a_name", "RegionType":"geojson", "RegionData":[}`))
	assert.NotNil(t, err)
}

func TestEmptyGeoJSONPolygon(t *testing.T) {
	_, _, _, _, err := parseInput(strings.NewReader(`{"Name":"a_name", "RegionType":"geojson", "RegionData":{"type":"Polygon","coordinates":[]}}`))
	assert.NotNil(t, err)
}
func TestEmptyGeoJSONMultiPolygon(t *testing.T) {
	_, _, _, _, err := parseInput(strings.NewReader(`{"Name":"a_name", "RegionType":"geojson", "RegionData":{"type":"MultiPolygon","coordinates":[]}}`))
	assert.NotNil(t, err)
}

func TestGeoJSONPolygonTooFewCoords(t *testing.T) {
	_, _, _, _, err := parseInput(strings.NewReader(`{"Name":"a_name", "RegionType":"geojson", "RegionData":{"type":"Polygon","coordinates":[[[0,0],[1,1],[0,0]]]}}`))
	assert.NotNil(t, err)
}
func TestGeoJSONMultiPolygonTooFewCoords(t *testing.T) {
	_, _, _, _, err := parseInput(strings.NewReader(`{"Name":"a_name", "RegionType":"geojson", "RegionData":{"type":"MultiPolygon","coordinates":[[]]}}`))
	assert.NotNil(t, err)
	_, _, _, _, err = parseInput(strings.NewReader(`{"Name":"a_name", "RegionType":"geojson", "RegionData":{"type":"MultiPolygon","coordinates":[[[],[]]]}}`))
	assert.NotNil(t, err)
}
