package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type ContentType string

const (
	Anime   ContentType = "anime"
	Series  ContentType = "series"
	Movie   ContentType = "movie"
	Unknown ContentType = "unknown"
)

func detectType(q string) (ContentType, error) {
	fmt.Print("detecting... ")
	u := "http://www.omdbapi.com/?t=" + url.QueryEscape(q) + "&apikey=trilogy"
	resp, err := http.Get(u)
	if err != nil {
		fmt.Println("unknown")
		return Unknown, nil
	}
	defer resp.Body.Close()

	var r struct {
		Type, Genre, Country, Response string
	}
	if json.NewDecoder(resp.Body).Decode(&r); r.Response == "False" {
		fmt.Println("unknown")
		return Unknown, nil
	}

	res := Unknown
	if r.Type == "movie" {
		res = Movie
	} else if r.Type == "series" {
		if strings.Contains(r.Genre, "Animation") && strings.Contains(r.Country, "Japan") {
			res = Anime
		} else {
			res = Series
		}
	}
	fmt.Println(res)
	return res, nil
}
