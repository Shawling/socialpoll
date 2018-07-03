package main

import (
	"net/http"

	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

// 这些 tag 表示这个结构体的字段在不同解析方式时名称不同（但值相同）
type poll struct {
	ID      bson.ObjectId  `bson:"_id" json:"id"`
	Title   string         `json:"title"`
	Options []string       `json:"options"`
	Result  map[string]int `json:"results,omitempty"`
	APIKey  string         `json:"apikey"`
}

func (s *Server) handlePolls(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		s.handlePollsGet(w, r)
	case "POST":
		s.handlePollsPost(w, r)
	case "DELETE":
		s.handlePollsDelete(w, r)
	case "OPTIONS":
		w.Header().Add("Access-Control-Allow-Methods", "DELETE")
		respond(w, r, http.StatusOK, nil)
		return
	default:
		respondHTTPErr(w, r, http.StatusNotFound)
	}
}

func (s *Server) handlePollsGet(w http.ResponseWriter,
	r *http.Request) {
	session := s.db.Copy()
	defer session.Copy()
	c := session.DB("ballots").C("polls")
	var q *mgo.Query
	p := NewPath(r.URL.Path)
	if p.HasID() {
		q = c.FindId(bson.ObjectIdHex(p.ID))
	} else {
		q = c.Find(nil)
	}
	var result []*poll
	if err := q.All(&result); err != nil {
		respondErr(w, r, http.StatusInternalServerError, err)
		return
	}
	respond(w, r, http.StatusOK, &result)
}

func (s *Server) handlePollsPost(w http.ResponseWriter,
	r *http.Request) {
	session := s.db.Copy()
	defer session.Close()
	c := session.DB("ballots").C("polls")
	var p poll
	if err := decodeBody(r, &p); err != nil {
		respondErr(w, r, http.StatusBadRequest, "failed to read poll from request: ", err)
		return
	}
	apiKey, ok := APIKey(r.Context())
	if ok {
		p.APIKey = apiKey
	}
	p.ID = bson.NewObjectId()
	if err := c.Insert(p); err != nil {
		respondErr(w, r, http.StatusInternalServerError, "failed to insert poll", err)
		return
	}
	// Location header 表示了新建的 resource 所在的位置
	w.Header().Set("Location", "polls/"+p.ID.Hex())
	respond(w, r, http.StatusCreated, nil)
}

func (s *Server) handlePollsDelete(w http.ResponseWriter,
	r *http.Request) {
	sesssion := s.db.Copy()
	defer sesssion.Close()
	c := sesssion.DB("ballots").C("polls")
	p := NewPath(r.URL.Path)
	if !p.HasID() {
		respondErr(w, r, http.StatusMethodNotAllowed, "Cannot delete poll without ID specify")
		return
	}
	if err := c.RemoveId(bson.ObjectIdHex(p.ID)); err != nil {
		respondErr(w, r, http.StatusInternalServerError, "failed to delete poll", err)
		return
	}
	respond(w, r, http.StatusOK, nil)
}
