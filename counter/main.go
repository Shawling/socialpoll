package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"github.com/bitly/go-nsq"
)

var fatalErr error

func fatal(e error) {
	fmt.Println(e)
	flag.PrintDefaults()
	fatalErr = e
}

const updataDuration = 1 * time.Second

func main() {
	// 将这个函数放在最后执行，保证在此之前可以先关闭数据库等链接，同时给出1的错误码
	defer func() {
		if fatalErr != nil {
			os.Exit(1)
		}
	}()

	log.Println("Connecting to db...")
	db, err := mgo.Dial("localhost")
	if err != nil {
		fatal(err)
		return
	}

	defer func() {
		log.Println("Closing db connection...")
		db.Close()
	}()

	pollData := db.DB("ballots").C("polls")

	var (
		counts     map[string]int
		countslock sync.Mutex
	)

	log.Println("Connecting to nsq...")
	q, err := nsq.NewConsumer("votes", "counter", nsq.NewConfig())
	if err != nil {
		fatal(err)
		return
	}

	q.AddHandler(nsq.HandlerFunc(func(m *nsq.Message) error {
		countslock.Lock()
		defer countslock.Unlock()
		if counts == nil {
			counts = make(map[string]int)
		}
		votes := string(m.Body)
		counts[votes]++
		return nil
	}))

	if err := q.ConnectToNSQLookupd("localhost:4161"); err != nil {
		fatal(err)
		return
	}

	ticker := time.NewTicker(updataDuration)
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	for {
		select {
		case <-ticker.C:
			doCount(&countslock, &counts, pollData)
		case <-stopChan:
			ticker.Stop()
			q.Stop()
		case <-q.StopChan:
			return
		}
	}
}

func doCount(countslock *sync.Mutex, counts *map[string]int, pollData *mgo.Collection) {
	countslock.Lock()
	defer countslock.Unlock()
	if len(*counts) == 0 {
		log.Println("No new vates, skipping db update")
		return
	}
	log.Println("Updating db...")
	log.Println(*counts)
	ok := true
	for option, count := range *counts {
		sel := bson.M{"options": bson.M{"$in": []string{option}}}
		up := bson.M{"$inc": bson.M{"result." + option: count}}
		if _, err := pollData.UpdateAll(sel, up); err != nil {
			log.Println("failed to update: ", err)
			ok = false
		}
	}
	if ok {
		log.Println("Finished updating db...")
		*counts = nil
	}
}
