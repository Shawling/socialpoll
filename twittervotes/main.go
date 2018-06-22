package main

import (
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/bitly/go-nsq"

	"gopkg.in/mgo.v2"
)

func main() {
	// 用锁保证多线程安全修改 stop 变量，用 变量名+lock 表示这个某个变量的锁
	var stoplock sync.Mutex
	stop := false

	// 带一个缓冲区的 channel ,使 channel的写入操作所在的  goroutine 不会因为执行接收操作的 goroutine 未就绪而被 block。同理，不带缓冲区的 channle 起到同步作用
	stopChan := make(chan struct{}, 1)
	signalChan := make(chan os.Signal, 1)

	go func() {
		// 等待接收退出 os.Signal
		<-signalChan
		stoplock.Lock()
		stop = true
		stoplock.Unlock()
		log.Println("Stopping...")
		stopChan <- struct{}{}
		closeConn()
	}()

	// 将系统的 interrupt, termination 信号发送到 signalChan
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	if err := dialdb("localhost"); err != nil {
		log.Fatalln("failed to dial MongoDB: ", err)
	}
	defer closedb()

	// start
	votes := make(chan string)
	publisherStoppenChan := publishVotes(votes)
	twitterStoppedChan := startTwitterStream(stopChan, votes)
	go func() {
		// 每分钟关闭一次 conn ,重新连接，用于从数据库中重新获取 options。如果程序停止，则停止 goroutine
		for {
			time.Sleep(1 * time.Minute)
			closeConn()
			stoplock.Lock()
			if stop {
				stoplock.Unlock()
				return
			}
			stoplock.Unlock()
		}
	}()
	//两个阻塞读操作等待关闭成功
	<-twitterStoppedChan
	close(votes)
	<-publisherStoppenChan
}

var db *mgo.Session

func dialdb(url string) error {
	log.Println("dialing mongodb: " + url)
	db, err := mgo.Dial(url)
	return err
}

func closedb() {
	db.Close()
	log.Println("closed db connection")
}

type poll struct {
	Options []string
}

func loadOptions() ([]string, error) {
	var options []string
	iter := db.DB("ballots").C("polls").Find(nil).Iter()
	//使用迭代器访问记录，无需将记录全部加载
	var p poll
	for iter.Next(&p) {
		options = append(options, p.Options...)
	}
	iter.Close()
	return options, iter.Err()
}

func publishVotes(votes <-chan string) <-chan struct{} {
	stoppedchan := make(chan struct{}, 1)
	pub, _ := nsq.NewProducer("localhost:4150", nsq.NewConfig())
	go func() {
		for vote := range votes {
			pub.Publish("votes", []byte(vote))
		}
		log.Println("Publisher: Stoping")
		pub.Stop()
		log.Println("Publisher: Stopped")
		stoppedchan <- struct{}{}
	}()
	return stoppedchan
}
