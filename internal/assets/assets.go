package assets

import (
	"bytes"
	_ "embed"
	"image"
	_ "image/png"
)

//go:embed hero.png
var HeroPNG []byte

// GetHeroImage returns the decoded hero image
func GetHeroImage() (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(HeroPNG))
	return img, err
}
