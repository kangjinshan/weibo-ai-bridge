package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

func main() {
	appID := "5288006887407627"
	appSecret := "a247bbc408e08e81e08f1baabcae7ae42cf4e4c9d9d30a4c9e6d8402200cb2d5"
	
	// 方法1：直接传递参数
	timestamp := time.Now().Unix()
	apiURL := fmt.Sprintf("http://open-im.api.weibo.com/open/auth/ws_token?app_id=%s&app_secret=%s&timestamp=%d",
		appID, appSecret, timestamp)
	
	fmt.Printf("Testing Method 1 (GET): %s\n", apiURL)
	resp, err := http.Get(apiURL)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("Response: %s\n\n", string(body))
	resp.Body.Close()
	
	// 方法2：POST 请求
	fmt.Printf("Testing Method 2 (POST): %s\n", apiURL)
	resp2, err := http.Post(apiURL, "application/x-www-form-urlencoded", nil)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	body2, _ := io.ReadAll(resp2.Body)
	fmt.Printf("Response: %s\n\n", string(body2))
	resp2.Body.Close()
	
	// 方法3：使用签名
	signStr := fmt.Sprintf("%s%s%d", appID, appSecret, timestamp)
	hash := sha1.Sum([]byte(signStr))
	sign := hex.EncodeToString(hash[:])
	
	apiURL3 := fmt.Sprintf("http://open-im.api.weibo.com/open/auth/ws_token?app_id=%s&sign=%s&timestamp=%d",
		appID, sign, timestamp)
	
	fmt.Printf("Testing Method 3 (with sign): %s\n", apiURL3)
	resp3, err := http.Get(apiURL3)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	body3, _ := io.ReadAll(resp3.Body)
	fmt.Printf("Response: %s\n\n", string(body3))
	resp3.Body.Close()
	
	// 方法4：POST Form
	formData := url.Values{}
	formData.Add("app_id", appID)
	formData.Add("app_secret", appSecret)
	formData.Add("timestamp", fmt.Sprintf("%d", timestamp))
	
	fmt.Printf("Testing Method 4 (POST Form)\n")
	resp4, err := http.PostForm("http://open-im.api.weibo.com/open/auth/ws_token", formData)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	body4, _ := io.ReadAll(resp4.Body)
	fmt.Printf("Response: %s\n", string(body4))
	resp4.Body.Close()
}
