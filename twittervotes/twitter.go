package main

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/garyburd/go-oauth/oauth"
	"github.com/joeshaw/envdecode"
)

var (
	conn   net.Conn
	reader io.ReadCloser
)

// dial 确保只有一个连接实例
func dial(netw, addr string) (net.Conn, error) {
	if conn != nil {
		conn.Close()
		conn = nil
	}
	netc, err := net.DialTimeout(netw, addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	conn = netc
	return conn, nil
}

func closeConn() {
	if conn != nil {
		conn.Close()
	}
	if reader != nil {
		reader.Close()
	}
}

var (
	authClient *oauth.Client
	creds      *oauth.Credentials
)

func setupTwitterAuth() {
	var ts struct {
		ConsumerKey    string `env:"SP_TWITTER_KEY,required"`
		ConsumerSecret string `env:"SP_TWITTER_SECRET,required"`
		AccessToken    string `env:"SP_TWITTER_ACCESSTOKEN,required"`
		AccessSecret   string `env:"SP_TWITTER_ACCESSSECRET,required"`
	}
	if err := envdecode.Decode(&ts); err != nil {
		log.Fatalln(err)
	}
	creds = &oauth.Credentials{
		Token:  ts.AccessToken,
		Secret: ts.AccessSecret,
	}
	authClient = &oauth.Client{
		Credentials: oauth.Credentials{
			Token:  ts.ConsumerKey,
			Secret: ts.ConsumerSecret,
		},
	}
}

// 将只需执行一次操作相关的变量与 sync.Once 变量写在一起，方便阅读
var (
	authSetupOnce sync.Once
	httpClient    *http.Client
)

// 新建与 twitter 的连接
func makeRequest(req *http.Request, params url.Values) (*http.Response, error) {
	// 多次连接只需初始化一次
	authSetupOnce.Do(func() {
		setupTwitterAuth()
		httpClient = &http.Client{
			Transport: &http.Transport{
				Dial: dial,
			},
		}
	})
	formEnc := params.Encode()
	req.Header.Set("Content-Type", "application/x-www-form- urlencoded")
	req.Header.Set("Content-Length", strconv.Itoa(len(formEnc)))
	req.Header.Set("Authorization", authClient.AuthorizationHeader(creds,
		"POST",
		req.URL, params))
	return httpClient.Do(req)
}

type tweet struct {
	Text string
}

func readFromTwitter(votes chan<- string) {
	// 从 db 中获取所有 option
	options, err := loadOptions()
	if err != nil {
		log.Println("failed to load options: ", err)
		return
	}
	// url.Parse() URL.String() 可以生成符合格式的 url 字符串
	u, err := url.Parse("https://stream.twitter.com/1.1/statuses/filter.json")
	if err != nil {
		log.Println("creating filter request failed:", err)
		return
	}
	query := make(url.Values)
	query.Set("track", strings.Join(options, ","))
	req, err := http.NewRequest("POST", u.String(), strings.NewReader(query.Encode()))
	if err != nil {
		log.Println("creating filter request failed:", err)
		return
	}
	resp, err := makeRequest(req, query)
	if err != nil {
		log.Println("making request failed:", err)
		return
	}
	reader = resp.Body
	decoder := json.NewDecoder(reader)
	// Decode 方法可以使用循环从一个 Reader 中保持读取，遇到错误再推出
	for {
		var t tweet
		if err := decoder.Decode(&t); err != nil {
			log.Println("end reading from twitter: ", err)
			break
		}
		for _, option := range options {
			if strings.Contains(strings.ToLower(t.Text), strings.ToLower(option)) {
				log.Println("vote: ", option)
				votes <- option
			}
		}
	}
}

func startTwitterStream(stopchan <-chan struct{}, votes chan<- string) <-chan struct{} {
	stoppedchan := make(chan struct{}, 1)
	go func() {
		defer func() {
			stoppedchan <- struct{}{}
		}()
		//从 twitter 中读取，遇到连接关闭，无论是twitter 关闭还是自己关闭，都尝试重连，直到收到 stopchan 信号
		for {
			select {
			case <-stopchan:
				log.Println("stopping Twitter...")
				return
			default:
				log.Println("Querying Twitter...")
				readFromTwitter(votes)
				log.Println("	(waiting)")
				// 重连等待时间
				time.Sleep(10 * time.Second)
			}
		}
	}()
	return stoppedchan
}
