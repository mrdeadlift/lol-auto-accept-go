package detector

import (
	"fmt"
	"image"
	"image/png"
	"os"

	"github.com/kbinani/screenshot"
)

type Point struct {
	X, Y int
}

type ImageDetector struct {
	acceptTemplate   image.Image
	matchingTemplate image.Image
	lastScreenshot   *image.RGBA
	screenBounds     image.Rectangle
}

func NewImageDetector() *ImageDetector {
	return &ImageDetector{}
}

func (d *ImageDetector) LoadTemplates() error {
	// 承認ボタンのテンプレート読み込み
	acceptFile, err := os.Open("resources/accept_button.png")
	if err != nil {
		return fmt.Errorf("承認ボタンテンプレートの読み込み失敗: %v", err)
	}
	defer acceptFile.Close()

	acceptImg, err := png.Decode(acceptFile)
	if err != nil {
		return fmt.Errorf("承認ボタン画像のデコード失敗: %v", err)
	}
	d.acceptTemplate = acceptImg

	// マッチング画面のテンプレート読み込み
	matchingFile, err := os.Open("resources/matching.png")
	if err != nil {
		return fmt.Errorf("マッチングテンプレートの読み込み失敗: %v", err)
	}
	defer matchingFile.Close()

	matchingImg, err := png.Decode(matchingFile)
	if err != nil {
		return fmt.Errorf("マッチング画像のデコード失敗: %v", err)
	}
	d.matchingTemplate = matchingImg

	return nil
}

func (d *ImageDetector) CaptureScreen() (*image.RGBA, error) {
	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return nil, err
	}
	d.lastScreenshot = img
	d.screenBounds = bounds
	return img, nil
}

func (d *ImageDetector) GetScreenBounds() image.Rectangle {
	return d.screenBounds
}

func (d *ImageDetector) GetLastScreenshot() *image.RGBA {
	return d.lastScreenshot
}

func (d *ImageDetector) GetAcceptTemplate() image.Image {
	return d.acceptTemplate
}

func (d *ImageDetector) GetMatchingTemplate() image.Image {
	return d.matchingTemplate
}