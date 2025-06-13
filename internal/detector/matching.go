package detector

import (
	"image"
	"image/color"
)

// 高速マッチング画面検出（テンプレートマッチング + 特徴点ベース）
func (d *ImageDetector) FastDetectMatchingScreen(img *image.RGBA) bool {
	bounds := img.Bounds()
	
	// 1. テンプレートマッチングによる検出
	if d.matchingTemplate != nil {
		// 画面全体でマッチングテンプレートを検索（低い閾値で）
		searchArea := image.Rect(0, 0, bounds.Dx(), bounds.Dy()) // 全体を検索
		if pos := d.templateMatchFast(img, d.matchingTemplate, 0.6, searchArea, 1.0); pos != nil {
			return true
		}
		
		// 複数スケールでも試行
		scales := []float64{0.5, 0.7, 0.8, 1.2, 1.5, 2.0}
		for _, scale := range scales {
			if pos := d.templateMatchFast(img, d.matchingTemplate, 0.5, searchArea, scale); pos != nil {
				return true
			}
		}
	}
	
	// 2. 文字検出による方法（「対戦を検出中」の文字を探す）
	// 「対戦を検出中」の特徴的な色パターンを検出
	matchingTextCount := 0
	totalSamples := 0
	
	// 画面中央付近を重点的に検索
	centerX := bounds.Dx() / 2
	centerY := bounds.Dy() / 2
	searchRect := image.Rect(
		centerX-200, centerY-100,
		centerX+200, centerY+100,
	)
	
	// 境界チェック
	if searchRect.Min.X < 0 {
		searchRect.Min.X = 0
	}
	if searchRect.Min.Y < 0 {
		searchRect.Min.Y = 0
	}
	if searchRect.Max.X > bounds.Dx() {
		searchRect.Max.X = bounds.Dx()
	}
	if searchRect.Max.Y > bounds.Dy() {
		searchRect.Max.Y = bounds.Dy()
	}
	
	// 3ピクセルごとにサンプリング
	for y := searchRect.Min.Y; y < searchRect.Max.Y; y += 3 {
		for x := searchRect.Min.X; x < searchRect.Max.X; x += 3 {
			c := img.RGBAAt(x, y)
			totalSamples++
			
			// 「対戦を検出中」の文字の色（白っぽい色）を検出
			if c.R > 200 && c.G > 200 && c.B > 200 {
				matchingTextCount++
			}
		}
	}
	
	// 白い文字の割合をチェック
	if totalSamples > 0 {
		textRatio := float64(matchingTextCount) / float64(totalSamples)
		if textRatio > 0.05 { // 5%以上の白い文字があれば検出
			return true
		}
	}
	
	return false
}

// 高速テンプレートマッチング（最適化版）
func (d *ImageDetector) templateMatchFast(haystack *image.RGBA, needle image.Image, threshold float64, searchArea image.Rectangle, scale float64) *Point {
	needleBounds := needle.Bounds()
	needleWidth := int(float64(needleBounds.Dx()) * scale)
	needleHeight := int(float64(needleBounds.Dy()) * scale)
	
	// 検索範囲を制限
	haystackBounds := haystack.Bounds()
	if searchArea.Min.X < haystackBounds.Min.X {
		searchArea.Min.X = haystackBounds.Min.X
	}
	if searchArea.Min.Y < haystackBounds.Min.Y {
		searchArea.Min.Y = haystackBounds.Min.Y
	}
	if searchArea.Max.X > haystackBounds.Max.X {
		searchArea.Max.X = haystackBounds.Max.X
	}
	if searchArea.Max.Y > haystackBounds.Max.Y {
		searchArea.Max.Y = haystackBounds.Max.Y
	}
	
	var bestMatch *Point
	bestScore := threshold
	
	// より細かい検索間隔
	step := 2
	for y := searchArea.Min.Y; y <= searchArea.Max.Y-needleHeight; y += step {
		for x := searchArea.Min.X; x <= searchArea.Max.X-needleWidth; x += step {
			// 高速類似度計算（サンプリングベース）
			score := d.calculateSimilarityFast(haystack, needle, x, y, scale)
			if score > bestScore {
				bestScore = score
				bestMatch = &Point{X: x + needleWidth/2, Y: y + needleHeight/2}
			}
		}
	}
	
	return bestMatch
}

// 高速類似度計算（サンプリングベース）
func (d *ImageDetector) calculateSimilarityFast(haystack *image.RGBA, needle image.Image, offsetX, offsetY int, scale float64) float64 {
	needleBounds := needle.Bounds()
	totalSamples := 0
	matchingSamples := 0
	
	// より細かいサンプリング
	sampleStep := 2
	
	for y := 0; y < needleBounds.Dy(); y += sampleStep {
		for x := 0; x < needleBounds.Dx(); x += sampleStep {
			haystackX := offsetX + int(float64(x)*scale)
			haystackY := offsetY + int(float64(y)*scale)
			
			if haystackX >= haystack.Bounds().Max.X || haystackY >= haystack.Bounds().Max.Y {
				continue
			}
			
			haystackColor := haystack.RGBAAt(haystackX, haystackY)
			needleColor := needle.At(needleBounds.Min.X+x, needleBounds.Min.Y+y)
			
			nr, ng, nb, na := needleColor.RGBA()
			nr, ng, nb, na = nr>>8, ng>>8, nb>>8, na>>8
			
			// より緩い色類似度判定
			if d.colorsAreSimilarLoose(haystackColor, nr, ng, nb) {
				matchingSamples++
			}
			totalSamples++
		}
	}
	
	if totalSamples == 0 {
		return 0
	}
	
	return float64(matchingSamples) / float64(totalSamples)
}

// より緩い色類似度判定
func (d *ImageDetector) colorsAreSimilarLoose(c1 color.RGBA, r2, g2, b2 uint32) bool {
	dr := int(c1.R) - int(r2)
	dg := int(c1.G) - int(g2)
	db := int(c1.B) - int(b2)
	
	if dr < 0 {
		dr = -dr
	}
	if dg < 0 {
		dg = -dg
	}
	if db < 0 {
		db = -db
	}
	
	return (dr + dg + db) < 150 // より緩い閾値
}