# LoL Auto Accept - Go版

League of Legends のマッチング承認を自動化するデスクトップアプリケーション（Go言語版）

## 機能

- 自動マッチング承認
- ユーザーフレンドリーなGUIインターフェース
- 監視の開始/停止機能
- リアルタイムログ表示

## 必要な環境

- Go 1.21以上
- OpenCV 4.x（gocvライブラリ用）
- Windows/macOS/Linux対応

## インストール

1. リポジトリをクローン：
```bash
git clone https://github.com/your-username/lol-auto-accept-go.git
cd lol-auto-accept-go
```

2. 依存関係をインストール：
```bash
go mod tidy
```

3. OpenCVのインストール（必要に応じて）：
   - Windows: https://opencv.org/releases/ からダウンロード
   - macOS: `brew install opencv`
   - Linux: `sudo apt-get install libopencv-dev`

## 使用方法

1. アプリケーションを起動：
```bash
go run main.go
```

2. ブラウザが自動で開きます（http://localhost:8080）

3. League of Legends クライアントを起動

4. Webページで「監視開始」ボタンをクリックして自動承認を開始

5. マッチングが見つかったら自動的に承認ボタンがクリックされます

6. 「監視停止」ボタンで監視を終了

## 注意事項

- League of Legends クライアントが起動している必要があります
- 画面解像度によって認識精度が影響される場合があります
- 操作中にマウスが自動で動く場合があります
- アンチチートソフトウェアとの競合の可能性があります

## 技術仕様

- **GUI**: WebブラウザベースUI (WebSocket + HTTP)
- **スクリーンキャプチャ**: kbinani/screenshot
- **画像処理**: 純粋Goによるテンプレートマッチング
- **マウス制御**: PowerShell (Windows) / xdotool (Linux/macOS)
- **Webフレームワーク**: Gorilla Mux + WebSocket

## ライセンス

MIT License

## 免責事項

このアプリケーションの使用により発生した問題については、作者は一切の責任を負いません。
Riot Games の利用規約に従ってご使用ください。