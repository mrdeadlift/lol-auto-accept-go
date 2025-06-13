package main

import (
	"log"

	"lol-auto-accept/internal/app"
	"lol-auto-accept/internal/server"
)

func main() {
	// アプリケーションインスタンス作成
	application := app.NewApp()
	
	// サーバーインスタンス作成
	srv := server.NewServer(application)
	
	log.Println("LoL Auto Accept アプリを起動中...")
	log.Println("サーバー起動: http://localhost:8081")
	log.Println("最適化済み: 高速検出アルゴリズム搭載")
	
	// サーバー開始
	log.Fatal(srv.Start())
}