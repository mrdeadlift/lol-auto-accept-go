package detector

import (
	"image"
	"image/color"
)

// 高精度承認ボタン検出（複数手法併用）
func (d *ImageDetector) FastDetectAcceptButton(img *image.RGBA) *Point {
	bounds := img.Bounds()
	centerX := bounds.Dx() / 2
	centerY := bounds.Dy() / 2
	
	// より広い検索範囲（画面下部全体）
	searchArea := image.Rect(
		centerX-400, centerY-50,
		centerX+400, centerY+250,
	)
	
	// 境界チェック
	if searchArea.Min.X < 0 {
		searchArea.Min.X = 0
	}
	if searchArea.Min.Y < 0 {
		searchArea.Min.Y = 0
	}
	if searchArea.Max.X > bounds.Dx() {
		searchArea.Max.X = bounds.Dx()
	}
	if searchArea.Max.Y > bounds.Dy() {
		searchArea.Max.Y = bounds.Dy()
	}
	
	// 手法1: テンプレートマッチング（複数スケール・低閾値）
	scales := []float64{0.5, 0.6, 0.7, 0.8, 0.9, 1.0, 1.1, 1.2, 1.3, 1.5}
	thresholds := []float64{0.4, 0.5, 0.6, 0.7}
	
	var bestMatch *Point
	var bestScore float64
	
	for _, threshold := range thresholds {
		for _, scale := range scales {
			if pos := d.templateMatchFast(img, d.acceptTemplate, threshold, searchArea, scale); pos != nil {
				score := d.VerifyAcceptButton(img, pos, scale)
				if score > bestScore {
					bestScore = score
					bestMatch = pos
				}
			}
		}
	}
	
	// 手法2: 色ベース検出（青緑のボタン色を検出）
	if bestMatch == nil {
		bestMatch = d.detectButtonByColor(img, searchArea)
	}
	
	// 手法3: エッジ検出（ボタンの輪郭を検出）
	if bestMatch == nil {
		bestMatch = d.detectButtonByEdge(img, searchArea)
	}
	
	return bestMatch
}

// 色ベース検出（青緑のボタン色を検出）
func (d *ImageDetector) detectButtonByColor(img *image.RGBA, searchArea image.Rectangle) *Point {
	var bestCandidate *Point
	maxClusterSize := 0
	
	// 青緑色のピクセルをクラスタリング
	for y := searchArea.Min.Y; y < searchArea.Max.Y; y += 2 {
		for x := searchArea.Min.X; x < searchArea.Max.X; x += 2 {
			c := img.RGBAAt(x, y)
			
			// 承認ボタンの特徴的な青緑色を検出
			if d.isAcceptButtonColor(c) {
				// 周囲の類似色ピクセルをカウント
				clusterSize := d.countSimilarColorCluster(img, x, y, searchArea)
				if clusterSize > maxClusterSize && clusterSize > 50 {
					maxClusterSize = clusterSize
					bestCandidate = &Point{X: x, Y: y}
				}
			}
		}
	}
	
	return bestCandidate
}

// 承認ボタンの色判定
func (d *ImageDetector) isAcceptButtonColor(c color.RGBA) bool {
	// 承認ボタンの青緑色（複数パターン）
	return (c.G > c.R+20 && c.G > c.B+10 && c.G > 100) || // 緑が強い
		   (c.B > c.R+20 && c.G > c.R+10 && c.B > 80) ||   // 青が強い
		   (c.G > 120 && c.B > 80 && c.R < 100)              // 青緑色
}

// 類似色クラスタのサイズをカウント
func (d *ImageDetector) countSimilarColorCluster(img *image.RGBA, centerX, centerY int, bounds image.Rectangle) int {
	count := 0
	radius := 20
	
	for dy := -radius; dy <= radius; dy++ {
		for dx := -radius; dx <= radius; dx++ {
			x := centerX + dx
			y := centerY + dy
			
			if x >= bounds.Min.X && x < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
				c := img.RGBAAt(x, y)
				if d.isAcceptButtonColor(c) {
					count++
				}
			}
		}
	}
	
	return count
}

// エッジ検出による承認ボタン検出
func (d *ImageDetector) detectButtonByEdge(img *image.RGBA, searchArea image.Rectangle) *Point {
	// ボタンの矩形エッジを検出
	for y := searchArea.Min.Y; y < searchArea.Max.Y-50; y += 5 {
		for x := searchArea.Min.X; x < searchArea.Max.X-100; x += 5 {
			// 50x25のエリアでボタンらしい形状を検索
			if d.isButtonShape(img, x, y, 100, 50) {
				return &Point{X: x + 50, Y: y + 25} // 中心点を返す
			}
		}
	}
	
	return nil
}

// ボタンの形状判定
func (d *ImageDetector) isButtonShape(img *image.RGBA, x, y, width, height int) bool {
	bounds := img.Bounds()
	if x+width >= bounds.Max.X || y+height >= bounds.Max.Y {
		return false
	}
	
	edgeCount := 0
	totalPixels := 0
	
	// 周囲のエッジを検出
	for dy := 0; dy < height; dy += 2 {
		for dx := 0; dx < width; dx += 2 {
			px := x + dx
			py := y + dy
			
			if px < bounds.Max.X-1 && py < bounds.Max.Y-1 {
				c1 := img.RGBAAt(px, py)
				c2 := img.RGBAAt(px+1, py)
				c3 := img.RGBAAt(px, py+1)
				
				// エッジ検出（色の変化が大きい場所）
				if d.colorDifference(c1, c2) > 30 || d.colorDifference(c1, c3) > 30 {
					edgeCount++
				}
				totalPixels++
			}
		}
	}
	
	// エッジの密度でボタン判定
	return totalPixels > 0 && float64(edgeCount)/float64(totalPixels) > 0.15
}

// 色の差分計算
func (d *ImageDetector) colorDifference(c1, c2 color.RGBA) int {
	dr := int(c1.R) - int(c2.R)
	dg := int(c1.G) - int(c2.G)
	db := int(c1.B) - int(c2.B)
	
	if dr < 0 {
		dr = -dr
	}
	if dg < 0 {
		dg = -dg
	}
	if db < 0 {
		db = -db
	}
	
	return dr + dg + db
}

// 承認ボタンの詳細検証（より緩い条件）
func (d *ImageDetector) VerifyAcceptButton(img *image.RGBA, pos *Point, scale float64) float64 {
	if d.acceptTemplate == nil {
		return 0.5 // テンプレートがない場合でも基本スコアを返す
	}
	
	needleBounds := d.acceptTemplate.Bounds()
	needleWidth := int(float64(needleBounds.Dx()) * scale)
	needleHeight := int(float64(needleBounds.Dy()) * scale)
	
	// ボタン位置の中心から実際のテンプレート領域を計算
	startX := pos.X - needleWidth/2
	startY := pos.Y - needleHeight/2
	
	// 境界チェック
	if startX < 0 || startY < 0 || startX+needleWidth >= img.Bounds().Max.X || startY+needleHeight >= img.Bounds().Max.Y {
		return 0.3 // 境界外でも低いスコアで継続
	}
	
	// より詳細な類似度計算
	score := d.calculateDetailedSimilarity(img, d.acceptTemplate, startX, startY, scale)
	
	// 周囲の色も考慮してスコアを調整
	colorBonus := d.checkSurroundingColors(img, pos.X, pos.Y)
	return score + colorBonus
}

// 周囲の色をチェックしてスコアを調整
func (d *ImageDetector) checkSurroundingColors(img *image.RGBA, centerX, centerY int) float64 {
	bounds := img.Bounds()
	blueGreenCount := 0
	totalChecked := 0
	
	// 周囲20ピクセルの範囲をチェック
	for dy := -20; dy <= 20; dy += 4 {
		for dx := -20; dx <= 20; dx += 4 {
			x := centerX + dx
			y := centerY + dy
			
			if x >= 0 && x < bounds.Dx() && y >= 0 && y < bounds.Dy() {
				c := img.RGBAAt(x, y)
				if d.isAcceptButtonColor(c) {
					blueGreenCount++
				}
				totalChecked++
			}
		}
	}
	
	if totalChecked == 0 {
		return 0
	}
	
	// 青緑色の割合に応じてボーナススコア
	ratio := float64(blueGreenCount) / float64(totalChecked)
	if ratio > 0.3 {
		return 0.2 // 30%以上なら大きなボーナス
	} else if ratio > 0.1 {
		return 0.1 // 10%以上なら小さなボーナス
	}
	
	return 0
}

// 詳細類似度計算（ピクセル単位での厳密チェック）
func (d *ImageDetector) calculateDetailedSimilarity(haystack *image.RGBA, needle image.Image, offsetX, offsetY int, scale float64) float64 {
	needleBounds := needle.Bounds()
	totalPixels := 0
	matchingPixels := 0
	
	// 全ピクセルをチェック（サンプリングなし）
	for y := 0; y < needleBounds.Dy(); y++ {
		for x := 0; x < needleBounds.Dx(); x++ {
			haystackX := offsetX + int(float64(x)*scale)
			haystackY := offsetY + int(float64(y)*scale)
			
			if haystackX >= haystack.Bounds().Max.X || haystackY >= haystack.Bounds().Max.Y {
				continue
			}
			
			haystackColor := haystack.RGBAAt(haystackX, haystackY)
			needleColor := needle.At(needleBounds.Min.X+x, needleBounds.Min.Y+y)
			
			nr, ng, nb, na := needleColor.RGBA()
			nr, ng, nb, na = nr>>8, ng>>8, nb>>8, na>>8
			
			// より厳密な色判定
			if d.colorsAreSimilarStrict(haystackColor, nr, ng, nb) {
				matchingPixels++
			}
			totalPixels++
		}
	}
	
	if totalPixels == 0 {
		return 0
	}
	
	return float64(matchingPixels) / float64(totalPixels)
}

// 厳密な色類似度判定
func (d *ImageDetector) colorsAreSimilarStrict(c1 color.RGBA, r2, g2, b2 uint32) bool {
	// より厳しい閾値
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
	
	return (dr + dg + db) < 60 // より厳しい閾値
}