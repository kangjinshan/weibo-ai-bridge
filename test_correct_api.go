package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func main() {
	appID := "5288006887407627"
	appSecret := "a247bbc408e08e81e08f1baabcae7ae42cf4e4c9d9d30a4c9e6d8402200cb2d5"
	
	// 构建JSON请求体
	payload := map[string]string{
		"app_id":          appID,
		"app_secret": appSecret,
	}
	
	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("Error marshaling: %v\n", err)
		return
	}
	
	fmt.Printf("Request Body: %s\n", string(body))
	
	// 发送POST请求
	resp, err := http.Post(
		"http://open-im.api.weibo.com/open/auth/ws_token",
		"application/json",
		strings.NewReader(string(body)),
	)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer resp.Body.Close()
	
	respBody, _ := io.ReadAll(resp.Body)
	fmt.Printf("Response Status: %d\n", resp.StatusCode)
	fmt.Printf("Response Body: %s\n", string(respBody))
}
