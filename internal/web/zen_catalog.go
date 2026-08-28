package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MediaCard represents a standard item in a row/carousel.
type MediaCard struct {
	ID           int64    `json:"id"`
	IMDbID       string   `json:"imdb_id,omitempty"`
	Title        string   `json:"title"`
	OriginalName string   `json:"original_title,omitempty"`
	Overview     string   `json:"overview"`
	PosterPath   string   `json:"poster_path"`
	BackdropPath string   `json:"backdrop_path"`
	MediaType    string   `json:"media_type"` // "movie" | "tv" | "anime"
	VoteAverage  float64  `json:"vote_average"`
	ReleaseDate  string   `json:"release_date"`
	Year         int      `json:"year"`
	Genres       []string `json:"genres"`
	Quality      string   `json:"quality"`
}

// MediaDetails holds full information for a movie or TV show.
type MediaDetails struct {
	MediaCard
	Tagline         string            `json:"tagline,omitempty"`
	Runtime         int               `json:"runtime,omitempty"`
	Director        string            `json:"director,omitempty"`
	Cast            []CastMember      `json:"cast"`
	Seasons         []SeasonInfo      `json:"seasons,omitempty"`
	Recommendations []MediaCard       `json:"recommendations"`
	TrailerKey      string            `json:"trailer_key,omitempty"`
	StreamingLinks  map[string]string `json:"streaming_links"`
}

type CastMember struct {
	Name        string `json:"name"`
	Character   string `json:"character"`
	ProfilePath string `json:"profile_path"`
}

type SeasonInfo struct {
	SeasonNumber int    `json:"season_number"`
	Name         string `json:"name"`
	EpisodeCount int    `json:"episode_count"`
	PosterPath   string `json:"poster_path,omitempty"`
}

type EpisodeInfo struct {
	EpisodeNumber int     `json:"episode_number"`
	SeasonNumber  int     `json:"season_number"`
	Name          string  `json:"name"`
	Overview      string  `json:"overview"`
	StillPath     string  `json:"still_path"`
	Runtime       int     `json:"runtime"`
	AirDate       string  `json:"air_date"`
	VoteAverage   float64 `json:"vote_average"`
}

type HomeResponse struct {
	Spotlight      []MediaCard `json:"spotlight"`
	TrendingMovies []MediaCard `json:"trending_movies"`
	TrendingTV     []MediaCard `json:"trending_tv"`
	Anime          []MediaCard `json:"anime"`
	Music          []MediaCard `json:"music"`
	TopRatedMovies []MediaCard `json:"top_rated_movies"`
	TopRatedTV     []MediaCard `json:"top_rated_tv"`
	ActionSciFi    []MediaCard `json:"action_scifi"`
}

var (
	webCacheMu sync.RWMutex
	webCache   = make(map[string]cachedEntry)
)

type cachedEntry struct {
	data      any
	expiresAt time.Time
}

func getWebCache(key string) (any, bool) {
	webCacheMu.RLock()
	defer webCacheMu.RUnlock()
	entry, ok := webCache[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.data, true
}

func setWebCache(key string, data any, ttl time.Duration) {
	webCacheMu.Lock()
	defer webCacheMu.Unlock()
	if len(webCache) > 1000 {
		webCache = make(map[string]cachedEntry)
	}
	webCache[key] = cachedEntry{
		data:      data,
		expiresAt: time.Now().Add(ttl),
	}
}

var sharedClient = &http.Client{
	Timeout: 4 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	},
}

// Built-in Season Registry for instant accurate season counts
var knownSeriesSeasons = map[string]int{
	"tt0944947":  8,  // Game of Thrones (8 Seasons)
	"tt0903747":  5,  // Breaking Bad (5 Seasons)
	"tt4574334":  4,  // Stranger Things (4 Seasons)
	"tt3032476":  6,  // Better Call Saul (6 Seasons)
	"tt3581920":  1,  // The Last of Us (1 Season)
	"tt11198330": 2,  // House of the Dragon (2 Seasons)
	"tt1190634":  4,  // The Boys (4 Seasons)
	"tt0141842":  6,  // The Sopranos (6 Seasons)
	"tt0306414":  5,  // The Wire (5 Seasons)
	"tt2442560":  6,  // Peaky Blinders (6 Seasons)
	"tt7134908":  1,  // Chernobyl (1 Season)
	"tt2788316":  1,  // Shogun (1 Season)
	"tt11280740": 1,  // Severance (1 Season)
	"tt7660850":  4,  // Succession (4 Seasons)
	"tt5753856":  3,  // Dark (3 Seasons)
	"tt2356777":  4,  // True Detective (4 Seasons)
	"tt2306299":  6,  // Vikings (6 Seasons)
	"tt0773262":  8,  // Dexter (8 Seasons)
	"tt1475582":  4,  // Sherlock (4 Seasons)
	"tt2802850":  5,  // Fargo (5 Seasons)
	"tt0475784":  4,  // Westworld (4 Seasons)
	"tt0455275":  5,  // Prison Break (5 Seasons)
	"tt0108778":  10, // Friends (10 Seasons)
	"tt0386676":  9,  // The Office (9 Seasons)
	"tt2861424":  7,  // Rick and Morty (7 Seasons)
	"tt1520211":  11, // The Walking Dead (11 Seasons)
	"tt0460681":  15, // Supernatural (15 Seasons)
	"tt2560140":  4,  // Attack on Titan (4 Seasons)
	"tt9335498":  4,  // Demon Slayer (4 Seasons)
	"tt12343534": 2,  // Jujutsu Kaisen (2 Seasons)
	"tt0388629":  21, // One Piece (21 Seasons)
	"tt0877057":  1,  // Death Note (1 Season)
	"tt21209876": 1,  // Solo Leveling (1 Season)
	"tt1397514":  5,  // Fullmetal Alchemist (5 Seasons)
	"tt13616990": 1,  // Chainsaw Man (1 Season)
	"tt14986406": 2,  // Bleach TYBW (2 Seasons)
	"tt10233448": 2,  // Vinland Saga (2 Seasons)
	"tt12590266": 1,  // Cyberpunk Edgerunners (1 Season)
	"tt11126994": 2,  // Arcane (2 Seasons)
	"tt0213338":  1,  // Cowboy Bebop (1 Season)
	"tt2098220":  6,  // Hunter x Hunter (6 Seasons)
}

// Curated library of 100% verified high-res Hollywood films, TV shows & anime
var masterSpotlight = []MediaCard{
	{
		ID:           693134,
		IMDbID:       "tt15239678",
		Title:        "Dune: Part Two",
		Overview:     "Paul Atreides unites with Chani and the Fremen while seeking revenge against the conspirators who destroyed his family.",
		PosterPath:   "https://images.metahub.space/poster/medium/tt15239678/img",
		BackdropPath: "https://images.metahub.space/background/medium/tt15239678/img",
		MediaType:    "movie",
		VoteAverage:  8.5,
		ReleaseDate:  "2024-03-01",
		Year:         2024,
		Genres:       []string{"Sci-Fi", "Adventure", "Action"},
		Quality:      "4K",
	},
	{
		ID:           157336,
		IMDbID:       "tt0816692",
		Title:        "Interstellar",
		Overview:     "A team of explorers travel through a wormhole in space in an attempt to ensure humanity's survival.",
		PosterPath:   "https://images.metahub.space/poster/medium/tt0816692/img",
		BackdropPath: "https://images.metahub.space/background/medium/tt0816692/img",
		MediaType:    "movie",
		VoteAverage:  8.7,
		ReleaseDate:  "2014-11-05",
		Year:         2014,
		Genres:       []string{"Sci-Fi", "Adventure", "Drama"},
		Quality:      "4K",
	},
	{
		ID:           872585,
		IMDbID:       "tt15398776",
		Title:        "Oppenheimer",
		Overview:     "The story of J. Robert Oppenheimer's role in the development of the atomic bomb during World War II.",
		PosterPath:   "https://images.metahub.space/poster/medium/tt15398776/img",
		BackdropPath: "https://images.metahub.space/background/medium/tt15398776/img",
		MediaType:    "movie",
		VoteAverage:  8.9,
		ReleaseDate:  "2023-07-21",
		Year:         2023,
		Genres:       []string{"Biography", "Drama", "History"},
		Quality:      "4K",
	},
	{
		ID:           1399,
		IMDbID:       "tt0944947",
		Title:        "Game of Thrones",
		Overview:     "Nine noble families fight for control over the lands of Westeros, while an ancient enemy returns after being dormant for millennia.",
		PosterPath:   "https://images.metahub.space/poster/medium/tt0944947/img",
		BackdropPath: "https://images.metahub.space/background/medium/tt0944947/img",
		MediaType:    "tv",
		VoteAverage:  9.2,
		ReleaseDate:  "2011-04-17",
		Year:         2011,
		Genres:       []string{"Drama", "Sci-Fi & Fantasy", "Action"},
		Quality:      "4K",
	},
	{
		ID:           66732,
		IMDbID:       "tt4574334",
		Title:        "Stranger Things",
		Overview:     "When a young boy vanishes, a small town uncovers a mystery involving secret experiments, terrifying supernatural forces and one strange little girl.",
		PosterPath:   "https://images.metahub.space/poster/medium/tt4574334/img",
		BackdropPath: "https://images.metahub.space/background/medium/tt4574334/img",
		MediaType:    "tv",
		VoteAverage:  8.6,
		ReleaseDate:  "2016-07-15",
		Year:         2016,
		Genres:       []string{"Sci-Fi", "Drama", "Mystery"},
		Quality:      "4K",
	},
	{
		ID:           1396,
		IMDbID:       "tt0903747",
		Title:        "Breaking Bad",
		Overview:     "A chemistry teacher diagnosed with inoperable lung cancer turns to manufacturing and selling methamphetamine with a former student.",
		PosterPath:   "https://images.metahub.space/poster/medium/tt0903747/img",
		BackdropPath: "https://images.metahub.space/background/medium/tt0903747/img",
		MediaType:    "tv",
		VoteAverage:  9.5,
		ReleaseDate:  "2008-01-20",
		Year:         2008,
		Genres:       []string{"Crime", "Drama", "Thriller"},
		Quality:      "4K",
	},
	{
		ID:           1429,
		IMDbID:       "tt2560140",
		Title:        "Attack on Titan",
		Overview:     "After his hometown is destroyed, young Eren Jaeger vows to cleanse the earth of the giant humanoid Titans.",
		PosterPath:   "https://images.metahub.space/poster/medium/tt2560140/img",
		BackdropPath: "https://images.metahub.space/background/medium/tt2560140/img",
		MediaType:    "anime",
		VoteAverage:  9.1,
		ReleaseDate:  "2013-04-07",
		Year:         2013,
		Genres:       []string{"Animation", "Action", "Fantasy"},
		Quality:      "4K",
	},
}

var masterMovies = []MediaCard{
	{
		ID: 693134, IMDbID: "tt15239678", Title: "Dune: Part Two", Year: 2024,
		Overview: "Paul Atreides unites with Chani and the Fremen while seeking revenge against the conspirators who destroyed his family.",
		PosterPath: "https://images.metahub.space/poster/medium/tt15239678/img", BackdropPath: "https://images.metahub.space/background/medium/tt15239678/img",
		MediaType: "movie", VoteAverage: 8.5, Genres: []string{"Sci-Fi", "Adventure", "Action"}, Quality: "4K",
	},
	{
		ID: 157336, IMDbID: "tt0816692", Title: "Interstellar", Year: 2014,
		Overview: "A team of explorers travel through a wormhole in space in an attempt to ensure humanity's survival.",
		PosterPath: "https://images.metahub.space/poster/medium/tt0816692/img", BackdropPath: "https://images.metahub.space/background/medium/tt0816692/img",
		MediaType: "movie", VoteAverage: 8.7, Genres: []string{"Sci-Fi", "Drama"}, Quality: "4K",
	},
	{
		ID: 872585, IMDbID: "tt15398776", Title: "Oppenheimer", Year: 2023,
		Overview: "The story of J. Robert Oppenheimer's role in the development of the atomic bomb during World War II.",
		PosterPath: "https://images.metahub.space/poster/medium/tt15398776/img", BackdropPath: "https://images.metahub.space/background/medium/tt15398776/img",
		MediaType: "movie", VoteAverage: 8.9, Genres: []string{"Biography", "Drama", "History"}, Quality: "4K",
	},
	{
		ID: 27205, IMDbID: "tt1375666", Title: "Inception", Year: 2010,
		Overview: "Cobb, a skilled thief who commits corporate espionage by infiltrating the subconscious of his targets, is offered a chance to regain his old life.",
		PosterPath: "https://images.metahub.space/poster/medium/tt1375666/img", BackdropPath: "https://images.metahub.space/background/medium/tt1375666/img",
		MediaType: "movie", VoteAverage: 8.8, Genres: []string{"Action", "Sci-Fi"}, Quality: "4K",
	},
	{
		ID: 155, IMDbID: "tt0468569", Title: "The Dark Knight", Year: 2008,
		Overview: "When the menace known as the Joker wreaks havoc and chaos on the people of Gotham, Batman must accept one of the greatest psychological and physical tests.",
		PosterPath: "https://images.metahub.space/poster/medium/tt0468569/img", BackdropPath: "https://images.metahub.space/background/medium/tt0468569/img",
		MediaType: "movie", VoteAverage: 9.0, Genres: []string{"Action", "Crime", "Drama"}, Quality: "4K",
	},
	{
		ID: 76600, IMDbID: "tt1630029", Title: "Avatar: The Way of Water", Year: 2022,
		Overview: "Set more than a decade after the events of the first film, learn the story of the Sully family and the trouble that follows them.",
		PosterPath: "https://images.metahub.space/poster/medium/tt1630029/img", BackdropPath: "https://images.metahub.space/background/medium/tt1630029/img",
		MediaType: "movie", VoteAverage: 7.8, Genres: []string{"Sci-Fi", "Adventure", "Action"}, Quality: "4K",
	},
	{
		ID: 569094, IMDbID: "tt9362722", Title: "Spider-Man: Across the Spider-Verse", Year: 2023,
		Overview: "Miles Morales catapults across the Multiverse, where he encounters a team of Spider-People charged with protecting its very existence.",
		PosterPath: "https://images.metahub.space/poster/medium/tt9362722/img", BackdropPath: "https://images.metahub.space/background/medium/tt9362722/img",
		MediaType: "movie", VoteAverage: 8.7, Genres: []string{"Animation", "Action", "Sci-Fi"}, Quality: "4K",
	},
	{
		ID: 414906, IMDbID: "tt1877830", Title: "The Batman", Year: 2022,
		Overview: "In his second year of fighting crime, Batman uncovers corruption in Gotham City that connects to his own family while facing a serial killer known as the Riddler.",
		PosterPath: "https://images.metahub.space/poster/medium/tt1877830/img", BackdropPath: "https://images.metahub.space/background/medium/tt1877830/img",
		MediaType: "movie", VoteAverage: 7.8, Genres: []string{"Crime", "Mystery", "Action"}, Quality: "4K",
	},
	{
		ID: 533535, IMDbID: "tt6263850", Title: "Deadpool & Wolverine", Year: 2024,
		Overview: "A listless Wade Wilson toils in civilian life with his days as the morally flexible mercenary behind him, until the Time Variance Authority pulls him into a new mission.",
		PosterPath: "https://images.metahub.space/poster/medium/tt6263850/img", BackdropPath: "https://images.metahub.space/background/medium/tt6263850/img",
		MediaType: "movie", VoteAverage: 8.0, Genres: []string{"Action", "Comedy", "Sci-Fi"}, Quality: "4K",
	},
	{
		ID: 945961, IMDbID: "tt18411490", Title: "Alien: Romulus", Year: 2024,
		Overview: "While scavenging the deep ends of a derelict space station, a group of young space colonizers come face to face with the most terrifying life form in the universe.",
		PosterPath: "https://images.metahub.space/poster/medium/tt18411490/img", BackdropPath: "https://images.metahub.space/background/medium/tt18411490/img",
		MediaType: "movie", VoteAverage: 7.3, Genres: []string{"Horror", "Sci-Fi"}, Quality: "4K",
	},
	{
		ID: 558449, IMDbID: "tt9603212", Title: "Gladiator II", Year: 2024,
		Overview: "Years after witnessing the death of the revered hero Maximus, Lucius must enter the Colosseum after his home is conquered by tyrannical Emperors.",
		PosterPath: "https://images.metahub.space/poster/medium/tt9603212/img", BackdropPath: "https://images.metahub.space/background/medium/tt9603212/img",
		MediaType: "movie", VoteAverage: 7.5, Genres: []string{"Action", "Adventure", "Drama"}, Quality: "4K",
	},
	{
		ID: 603, IMDbID: "tt0133093", Title: "The Matrix", Year: 1999,
		Overview: "Set in the 22nd century, The Matrix tells the story of a computer hacker who joins a group of underground insurgents fighting the vast and powerful computers.",
		PosterPath: "https://images.metahub.space/poster/medium/tt0133093/img", BackdropPath: "https://images.metahub.space/background/medium/tt0133093/img",
		MediaType: "movie", VoteAverage: 8.7, Genres: []string{"Sci-Fi", "Action"}, Quality: "4K",
	},
}

var masterSeries = []MediaCard{
	{
		ID: 1399, IMDbID: "tt0944947", Title: "Game of Thrones", Year: 2011,
		Overview: "Nine noble families fight for control over the lands of Westeros, while an ancient enemy returns after being dormant for millennia.",
		PosterPath: "https://images.metahub.space/poster/medium/tt0944947/img", BackdropPath: "https://images.metahub.space/background/medium/tt0944947/img",
		MediaType: "tv", VoteAverage: 9.2, Genres: []string{"Drama", "Sci-Fi & Fantasy", "Action"}, Quality: "4K",
	},
	{
		ID: 1396, IMDbID: "tt0903747", Title: "Breaking Bad", Year: 2008,
		Overview: "A chemistry teacher diagnosed with inoperable lung cancer turns to manufacturing and selling methamphetamine with a former student.",
		PosterPath: "https://images.metahub.space/poster/medium/tt0903747/img", BackdropPath: "https://images.metahub.space/background/medium/tt0903747/img",
		MediaType: "tv", VoteAverage: 9.5, Genres: []string{"Crime", "Drama", "Thriller"}, Quality: "4K",
	},
	{
		ID: 66732, IMDbID: "tt4574334", Title: "Stranger Things", Year: 2016,
		Overview: "When a young boy vanishes, a small town uncovers a mystery involving secret experiments, terrifying supernatural forces and one strange little girl.",
		PosterPath: "https://images.metahub.space/poster/medium/tt4574334/img", BackdropPath: "https://images.metahub.space/background/medium/tt4574334/img",
		MediaType: "tv", VoteAverage: 8.6, Genres: []string{"Sci-Fi", "Drama", "Mystery"}, Quality: "4K",
	},
	{
		ID: 60059, IMDbID: "tt3032476", Title: "Better Call Saul", Year: 2015,
		Overview: "The trials and tribulations of criminal lawyer Jimmy McGill in the years leading up to his fateful run-in with Walter White and Jesse Pinkman.",
		PosterPath: "https://images.metahub.space/poster/medium/tt3032476/img", BackdropPath: "https://images.metahub.space/background/medium/tt3032476/img",
		MediaType: "tv", VoteAverage: 8.9, Genres: []string{"Crime", "Drama"}, Quality: "4K",
	},
	{
		ID: 100088, IMDbID: "tt3581920", Title: "The Last of Us", Year: 2023,
		Overview: "Twenty years after modern civilization has been destroyed, Joel is hired to smuggle Ellie, a 14-year-old girl, out of an oppressive quarantine zone.",
		PosterPath: "https://images.metahub.space/poster/medium/tt3581920/img", BackdropPath: "https://images.metahub.space/background/medium/tt3581920/img",
		MediaType: "tv", VoteAverage: 8.8, Genres: []string{"Drama", "Sci-Fi & Fantasy", "Action"}, Quality: "4K",
	},
	{
		ID: 94997, IMDbID: "tt11198330", Title: "House of the Dragon", Year: 2022,
		Overview: "The Targaryen dynasty is at the absolute apex of its power, with more than 15 dragons under their yoke. Most empires fall from such heights.",
		PosterPath: "https://images.metahub.space/poster/medium/tt11198330/img", BackdropPath: "https://images.metahub.space/background/medium/tt11198330/img",
		MediaType: "tv", VoteAverage: 8.5, Genres: []string{"Action & Adventure", "Drama", "Sci-Fi & Fantasy"}, Quality: "4K",
	},
	{
		ID: 76479, IMDbID: "tt1190634", Title: "The Boys", Year: 2019,
		Overview: "A fun and irreverent take on what happens when superheroes—who are as popular as celebrities—abuse their superpowers rather than use them for good.",
		PosterPath: "https://images.metahub.space/poster/medium/tt1190634/img", BackdropPath: "https://images.metahub.space/background/medium/tt1190634/img",
		MediaType: "tv", VoteAverage: 8.7, Genres: []string{"Sci-Fi & Fantasy", "Action & Adventure"}, Quality: "4K",
	},
	{
		ID: 1398, IMDbID: "tt0141842", Title: "The Sopranos", Year: 1999,
		Overview: "New Jersey mob boss Tony Soprano deals with personal and professional issues in his home and business life that affect his mental state.",
		PosterPath: "https://images.metahub.space/poster/medium/tt0141842/img", BackdropPath: "https://images.metahub.space/background/medium/tt0141842/img",
		MediaType: "tv", VoteAverage: 9.2, Genres: []string{"Drama", "Crime"}, Quality: "4K",
	},
	{
		ID: 60574, IMDbID: "tt2442560", Title: "Peaky Blinders", Year: 2013,
		Overview: "A gangster family epic set in 1900s England, centering on a gang who sew razor blades in the peaks of their caps, and their fierce boss Tommy Shelby.",
		PosterPath: "https://images.metahub.space/poster/medium/tt2442560/img", BackdropPath: "https://images.metahub.space/background/medium/tt2442560/img",
		MediaType: "tv", VoteAverage: 8.8, Genres: []string{"Crime", "Drama"}, Quality: "4K",
	},
	{
		ID: 126308, IMDbID: "tt2788316", Title: "Shogun", Year: 2024,
		Overview: "In Japan in the year 1600, Lord Yoshii Toranaga is fighting for his life as his enemies on the Council of Regents unite against him.",
		PosterPath: "https://images.metahub.space/poster/medium/tt2788316/img", BackdropPath: "https://images.metahub.space/background/medium/tt2788316/img",
		MediaType: "tv", VoteAverage: 8.7, Genres: []string{"Drama", "War & Politics"}, Quality: "4K",
	},
	{
		ID: 87108, IMDbID: "tt7134908", Title: "Chernobyl", Year: 2019,
		Overview: "The true story of one of the worst man-made catastrophes in history: the catastrophic nuclear accident at Chernobyl.",
		PosterPath: "https://images.metahub.space/poster/medium/tt7134908/img", BackdropPath: "https://images.metahub.space/background/medium/tt7134908/img",
		MediaType: "tv", VoteAverage: 9.4, Genres: []string{"Drama", "History"}, Quality: "4K",
	},
	{
		ID: 93405, IMDbID: "tt11280740", Title: "Severance", Year: 2022,
		Overview: "Mark leads a team of office workers whose memories have been surgically divided between their work and personal lives.",
		PosterPath: "https://images.metahub.space/poster/medium/tt11280740/img", BackdropPath: "https://images.metahub.space/background/medium/tt11280740/img",
		MediaType: "tv", VoteAverage: 8.7, Genres: []string{"Drama", "Sci-Fi & Fantasy", "Mystery"}, Quality: "4K",
	},
	{
		ID: 76331, IMDbID: "tt7660850", Title: "Succession", Year: 2018,
		Overview: "The Roy family is known for controlling the biggest media and entertainment company in the world. However, their world changes when their aging father steps down.",
		PosterPath: "https://images.metahub.space/poster/medium/tt7660850/img", BackdropPath: "https://images.metahub.space/background/medium/tt7660850/img",
		MediaType: "tv", VoteAverage: 8.9, Genres: []string{"Drama"}, Quality: "4K",
	},
	{
		ID: 70523, IMDbID: "tt5753856", Title: "Dark", Year: 2017,
		Overview: "A missing child sets four families on a frantic hunt for answers as they unearth a mind-bending mystery that spans three generations.",
		PosterPath: "https://images.metahub.space/poster/medium/tt5753856/img", BackdropPath: "https://images.metahub.space/background/medium/tt5753856/img",
		MediaType: "tv", VoteAverage: 8.7, Genres: []string{"Sci-Fi & Fantasy", "Drama", "Mystery"}, Quality: "4K",
	},
	{
		ID: 44217, IMDbID: "tt2306299", Title: "Vikings", Year: 2013,
		Overview: "The adventures of Ragnar Lothbrok: the greatest hero of his age. The series tells the saga of Ragnar's band of Viking brothers and his family.",
		PosterPath: "https://images.metahub.space/poster/medium/tt2306299/img", BackdropPath: "https://images.metahub.space/background/medium/tt2306299/img",
		MediaType: "tv", VoteAverage: 8.5, Genres: []string{"Action & Adventure", "Drama"}, Quality: "4K",
	},
	{
		ID: 1400, IMDbID: "tt0773262", Title: "Dexter", Year: 2006,
		Overview: "Dexter Morgan, a blood spatter pattern analyst for the Miami Metro Police Department, leads a secret parallel life as a vigilante serial killer.",
		PosterPath: "https://images.metahub.space/poster/medium/tt0773262/img", BackdropPath: "https://images.metahub.space/background/medium/tt0773262/img",
		MediaType: "tv", VoteAverage: 8.7, Genres: []string{"Crime", "Drama", "Mystery"}, Quality: "4K",
	},
	{
		ID: 1668, IMDbID: "tt0108778", Title: "Friends", Year: 1994,
		Overview: "Six young people from New York City navigate the pitfalls of life, love and relationships in the 1990s.",
		PosterPath: "https://images.metahub.space/poster/medium/tt0108778/img", BackdropPath: "https://images.metahub.space/background/medium/tt0108778/img",
		MediaType: "tv", VoteAverage: 8.9, Genres: []string{"Comedy", "Romance"}, Quality: "4K",
	},
	{
		ID: 2316, IMDbID: "tt0386676", Title: "The Office", Year: 2005,
		Overview: "A mockumentary on a group of typical office workers, where the workday consists of ego clashes, inappropriate behavior, and tedium.",
		PosterPath: "https://images.metahub.space/poster/medium/tt0386676/img", BackdropPath: "https://images.metahub.space/background/medium/tt0386676/img",
		MediaType: "tv", VoteAverage: 9.0, Genres: []string{"Comedy"}, Quality: "4K",
	},
	{
		ID: 60625, IMDbID: "tt2861424", Title: "Rick and Morty", Year: 2013,
		Overview: "An animated series that follows the exploits of a super scientist and his not-so-bright grandson.",
		PosterPath: "https://images.metahub.space/poster/medium/tt2861424/img", BackdropPath: "https://images.metahub.space/background/medium/tt2861424/img",
		MediaType: "tv", VoteAverage: 9.1, Genres: []string{"Animation", "Comedy", "Sci-Fi"}, Quality: "4K",
	},
	{
		ID: 1402, IMDbID: "tt1520211", Title: "The Walking Dead", Year: 2010,
		Overview: "Sheriff's deputy Rick Grimes awakens from a coma to find a post-apocalyptic world dominated by flesh-eating zombies.",
		PosterPath: "https://images.metahub.space/poster/medium/tt1520211/img", BackdropPath: "https://images.metahub.space/background/medium/tt1520211/img",
		MediaType: "tv", VoteAverage: 8.3, Genres: []string{"Action & Adventure", "Drama", "Sci-Fi & Fantasy"}, Quality: "4K",
	},
	{
		ID: 1622, IMDbID: "tt0460681", Title: "Supernatural", Year: 2005,
		Overview: "Two brothers follow their father's footsteps as hunters, fighting evil supernatural beings of many kinds on earth.",
		PosterPath: "https://images.metahub.space/poster/medium/tt0460681/img", BackdropPath: "https://images.metahub.space/background/medium/tt0460681/img",
		MediaType: "tv", VoteAverage: 8.4, Genres: []string{"Drama", "Mystery", "Sci-Fi & Fantasy"}, Quality: "4K",
	},
}

var masterAnime = []MediaCard{
	{
		ID: 1429, IMDbID: "tt2560140", Title: "Attack on Titan", Year: 2013,
		Overview: "After his hometown is destroyed, young Eren Jaeger vows to cleanse the earth of the giant humanoid Titans.",
		PosterPath: "https://images.metahub.space/poster/medium/tt2560140/img", BackdropPath: "https://images.metahub.space/background/medium/tt2560140/img",
		MediaType: "anime", VoteAverage: 9.1, Genres: []string{"Animation", "Action", "Fantasy"}, Quality: "4K",
	},
	{
		ID: 85937, IMDbID: "tt9335498", Title: "Demon Slayer: Kimetsu no Yaiba", Year: 2019,
		Overview: "Tanjiro Kamado, a boy who loses his family to demons, embarks on a dangerous journey to turn his demonized sister Nezuko back into a human.",
		PosterPath: "https://images.metahub.space/poster/medium/tt9335498/img", BackdropPath: "https://images.metahub.space/background/medium/tt9335498/img",
		MediaType: "anime", VoteAverage: 8.7, Genres: []string{"Animation", "Action", "Fantasy"}, Quality: "4K",
	},
	{
		ID: 95479, IMDbID: "tt12343534", Title: "Jujutsu Kaisen", Year: 2020,
		Overview: "Yuji Itadori, a boy who swallows a cursed talisman—the finger of a demon—becomes cursed himself and enters a shaman school.",
		PosterPath: "https://images.metahub.space/poster/medium/tt12343534/img", BackdropPath: "https://images.metahub.space/background/medium/tt12343534/img",
		MediaType: "anime", VoteAverage: 8.6, Genres: []string{"Animation", "Action", "Fantasy"}, Quality: "4K",
	},
	{
		ID: 37854, IMDbID: "tt0388629", Title: "One Piece", Year: 1999,
		Overview: "Years ago, the fearsome Pirate King, Gol D. Roger was executed leaving behind a huge cache of treasure known as the 'One Piece'.",
		PosterPath: "https://images.metahub.space/poster/medium/tt0388629/img", BackdropPath: "https://images.metahub.space/background/medium/tt0388629/img",
		MediaType: "anime", VoteAverage: 9.0, Genres: []string{"Animation", "Action", "Adventure"}, Quality: "4K",
	},
	{
		ID: 13916, IMDbID: "tt0877057", Title: "Death Note", Year: 2006,
		Overview: "Light Yagami is a genius high school student who discovers the 'Death Note', a notebook that grants the user the ability to kill anyone.",
		PosterPath: "https://images.metahub.space/poster/medium/tt0877057/img", BackdropPath: "https://images.metahub.space/background/medium/tt0877057/img",
		MediaType: "anime", VoteAverage: 9.0, Genres: []string{"Animation", "Mystery", "Psychological"}, Quality: "4K",
	},
	{
		ID: 209867, IMDbID: "tt21209876", Title: "Solo Leveling", Year: 2024,
		Overview: "In a world where hunters must battle deadly monsters to protect humanity, weak hunter Sung Jinwoo is chosen by a mysterious quest program.",
		PosterPath: "https://images.metahub.space/poster/medium/tt21209876/img", BackdropPath: "https://images.metahub.space/background/medium/tt21209876/img",
		MediaType: "anime", VoteAverage: 8.5, Genres: []string{"Animation", "Action", "Fantasy"}, Quality: "4K",
	},
	{
		ID: 31911, IMDbID: "tt1397514", Title: "Fullmetal Alchemist: Brotherhood", Year: 2009,
		Overview: "Two brothers search for a Philosopher's Stone after an attempt to revive their deceased mother goes wrong.",
		PosterPath: "https://images.metahub.space/poster/medium/tt1397514/img", BackdropPath: "https://images.metahub.space/background/medium/tt1397514/img",
		MediaType: "anime", VoteAverage: 9.1, Genres: []string{"Animation", "Action", "Adventure"}, Quality: "4K",
	},
	{
		ID: 114410, IMDbID: "tt13616990", Title: "Chainsaw Man", Year: 2022,
		Overview: "Denji is a teenage boy living with a Chainsaw Devil named Pochita. Due to the debt his father left behind, he has been living a rock-bottom life.",
		PosterPath: "https://images.metahub.space/poster/medium/tt13616990/img", BackdropPath: "https://images.metahub.space/background/medium/tt13616990/img",
		MediaType: "anime", VoteAverage: 8.5, Genres: []string{"Animation", "Action", "Supernatural"}, Quality: "4K",
	},
	{
		ID: 94605, IMDbID: "tt11126994", Title: "Arcane", Year: 2021,
		Overview: "Set in the utopian region of Piltover and the oppressed underground of Zaun, the story follows the origins of two iconic League champions-and the power that will tear them apart.",
		PosterPath: "https://images.metahub.space/poster/medium/tt11126994/img", BackdropPath: "https://images.metahub.space/background/medium/tt11126994/img",
		MediaType: "anime", VoteAverage: 9.0, Genres: []string{"Animation", "Sci-Fi & Fantasy", "Action"}, Quality: "4K",
	},
}

var masterMusic = []MediaCard{
	{
		ID: 1001, Title: "Cornfield Chase (Interstellar OST)", Year: 2014,
		Overview: "Hans Zimmer's iconic organ masterpiece from Christopher Nolan's Interstellar. Uncompressed dynamic studio master.",
		PosterPath: "https://images.unsplash.com/photo-1518709268805-4e9042af9f23?w=600&q=80",
		BackdropPath: "https://images.unsplash.com/photo-1506703719100-a0f3a48c0f86?w=1200&q=80",
		MediaType: "music", VoteAverage: 9.9, Genres: []string{"Soundtrack", "Classical", "Cinematic"}, Quality: "FLAC 24-bit",
	},
	{
		ID: 1002, Title: "Paul's Dream (Dune: Part Two)", Year: 2024,
		Overview: "Hans Zimmer · Electric desert choral tones and visceral cinematic percussion from Dune: Part Two.",
		PosterPath: "https://images.unsplash.com/photo-1509198397868-475647b2a1e5?w=600&q=80",
		BackdropPath: "https://images.unsplash.com/photo-1534447677768-be436bb09401?w=1200&q=80",
		MediaType: "music", VoteAverage: 9.7, Genres: []string{"Soundtrack", "Ambient", "Orchestral"}, Quality: "Dolby Atmos",
	},
	{
		ID: 1003, Title: "Can You Hear The Music (Oppenheimer)", Year: 2023,
		Overview: "Ludwig Göransson · Complex polyrhythmic 24-tempo violin sequence capturing the quantum realm.",
		PosterPath: "https://images.unsplash.com/photo-1507838153414-b4b713384a76?w=600&q=80",
		BackdropPath: "https://images.unsplash.com/photo-1451187580459-43490279c0fa?w=1200&q=80",
		MediaType: "music", VoteAverage: 9.8, Genres: []string{"Soundtrack", "Modern Classical"}, Quality: "FLAC 24-bit",
	},
	{
		ID: 1004, Title: "Starboy (feat. Daft Punk)", Year: 2016,
		Overview: "The Weeknd, Daft Punk · Electropop R&B anthem with analog synth lines and punchy 808 bass.",
		PosterPath: "https://images.unsplash.com/photo-1470225620780-dba8ba36b745?w=600&q=80",
		BackdropPath: "https://images.unsplash.com/photo-1514525253161-7a46d19cd819?w=1200&q=80",
		MediaType: "music", VoteAverage: 9.4, Genres: []string{"Synthpop", "R&B", "Electronic"}, Quality: "320 kbps",
	},
	{
		ID: 1005, Title: "Midnight City", Year: 2011,
		Overview: "M83 · Iconic synthwave anthem driven by shimmering vocal hooks and vintage saxophone solos.",
		PosterPath: "https://images.unsplash.com/photo-1511671782779-c97d3d27a1d4?w=600&q=80",
		BackdropPath: "https://images.unsplash.com/photo-1518709268805-4e9042af9f23?w=1200&q=80",
		MediaType: "music", VoteAverage: 9.6, Genres: []string{"Synthwave", "Indie Electronic"}, Quality: "FLAC",
	},
	{
		ID: 1006, Title: "Kun Faya Kun (Rockstar)", Year: 2011,
		Overview: "A.R. Rahman, Mohit Chauhan, Javed Ali · Sufi spiritual masterpiece with acoustic strings and harmonium.",
		PosterPath: "https://images.unsplash.com/photo-1465847899084-d164df4dedc6?w=600&q=80",
		BackdropPath: "https://images.unsplash.com/photo-1501386761578-eac5c94b800a?w=1200&q=80",
		MediaType: "music", VoteAverage: 9.9, Genres: []string{"Sufi", "Bollywood", "Acoustic"}, Quality: "Lossless",
	},
	{
		ID: 1007, Title: "Lofi Hip Hop Beats (Relax / Study)", Year: 2024,
		Overview: "ChilledCow / Lofi Girl · Warm vinyl crackle, mellow Fender Rhodes chords, and soothing downtempo beats.",
		PosterPath: "https://images.unsplash.com/photo-1534447677768-be436bb09401?w=600&q=80",
		BackdropPath: "https://images.unsplash.com/photo-1518709268805-4e9042af9f23?w=1200&q=80",
		MediaType: "music", VoteAverage: 9.5, Genres: []string{"Lofi", "Chillhop", "Instrumental"}, Quality: "320 kbps",
	},
	{
		ID: 1008, Title: "Time (Inception Live at Prague)", Year: 2010,
		Overview: "Hans Zimmer · Ascending orchestral crescendo exploring the boundaries of dreams and memory.",
		PosterPath: "https://images.unsplash.com/photo-1511671782779-c97d3d27a1d4?w=600&q=80",
		BackdropPath: "https://images.unsplash.com/photo-1451187580459-43490279c0fa?w=1200&q=80",
		MediaType: "music", VoteAverage: 9.9, Genres: []string{"Soundtrack", "Epic", "Cinematic"}, Quality: "FLAC 24-bit",
	},
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	resp := HomeResponse{
		Spotlight:      masterSpotlight,
		TrendingMovies: masterMovies,
		TrendingTV:     masterSeries,
		Anime:          masterAnime,
		Music:          masterMusic,
		TopRatedMovies: masterMovies,
		TopRatedTV:     masterSeries,
		ActionSciFi:    masterMovies,
	}

	setWebCache("home_feed_v4", resp, 30*time.Minute)
	writeJSON(w, resp)
}

func (s *Server) handleDetails(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimSpace(r.URL.Query().Get("id"))
	imdb := strings.TrimSpace(r.URL.Query().Get("imdb"))
	mType := strings.TrimSpace(r.URL.Query().Get("type"))
	if mType == "" {
		mType = "movie"
	}

	cacheKey := fmt.Sprintf("details_v4_%s_%s_%s", idStr, imdb, mType)
	if data, ok := getWebCache(cacheKey); ok {
		writeJSON(w, data)
		return
	}

	// Search in master library first
	var matched *MediaCard
	for _, m := range masterSpotlight {
		if (imdb != "" && m.IMDbID == imdb) || (idStr != "" && fmt.Sprintf("%d", m.ID) == idStr) {
			matched = &m
			break
		}
	}
	if matched == nil {
		for _, m := range masterMovies {
			if (imdb != "" && m.IMDbID == imdb) || (idStr != "" && fmt.Sprintf("%d", m.ID) == idStr) {
				matched = &m
				break
			}
		}
	}
	if matched == nil {
		for _, m := range masterSeries {
			if (imdb != "" && m.IMDbID == imdb) || (idStr != "" && fmt.Sprintf("%d", m.ID) == idStr) {
				matched = &m
				break
			}
		}
	}
	if matched == nil {
		for _, m := range masterAnime {
			if (imdb != "" && m.IMDbID == imdb) || (idStr != "" && fmt.Sprintf("%d", m.ID) == idStr) {
				matched = &m
				break
			}
		}
	}

	details := MediaDetails{
		MediaCard: MediaCard{
			Title:       "Title",
			MediaType:   mType,
			VoteAverage: 8.5,
			Quality:     "4K UHD",
			Genres:      []string{"Drama", "Sci-Fi", "Action"},
		},
		StreamingLinks: generateStreamingLinks(idStr, imdb, mType, 1, 1),
	}

	if matched != nil {
		details.MediaCard = *matched
		details.Tagline = "Stream in 4K UHD with Dolby Atmos & Multi-Audio"
	}

	// Discover and populate all seasons for TV Series & Anime
	if mType == "tv" || mType == "series" || mType == "anime" || (matched != nil && (matched.MediaType == "tv" || matched.MediaType == "anime")) {
		seasonCount := knownSeriesSeasons[imdb]
		if seasonCount <= 0 {
			seasonCount = knownSeriesSeasons[details.IMDbID]
		}

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		var discoveredSeasons []SeasonInfo
		targetIMDb := imdb
		if targetIMDb == "" {
			targetIMDb = details.IMDbID
		}

		if targetIMDb != "" {
			u := fmt.Sprintf("https://v3-cinemeta.strem.io/meta/series/%s.json", url.PathEscape(targetIMDb))
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
			if err == nil {
				if resp, err := sharedClient.Do(req); err == nil {
					defer resp.Body.Close()
					var metaPayload struct {
						Meta struct {
							Videos []struct {
								Season  int `json:"season"`
								Episode int `json:"episode"`
							} `json:"videos"`
						} `json:"meta"`
					}
					if json.NewDecoder(resp.Body).Decode(&metaPayload) == nil {
						maxS := 0
						seasonEpCount := make(map[int]int)
						for _, v := range metaPayload.Meta.Videos {
							if v.Season > maxS {
								maxS = v.Season
							}
							seasonEpCount[v.Season]++
						}
						if maxS > 0 {
							for s := 1; s <= maxS; s++ {
								epCount := seasonEpCount[s]
								if epCount == 0 {
									epCount = 10
								}
								discoveredSeasons = append(discoveredSeasons, SeasonInfo{
									SeasonNumber: s,
									Name:         fmt.Sprintf("Season %d", s),
									EpisodeCount: epCount,
								})
							}
						}
					}
				}
			}
		}

		if len(discoveredSeasons) > 0 {
			details.Seasons = discoveredSeasons
		} else {
			if seasonCount <= 0 {
				seasonCount = 4
			}
			for s := 1; s <= seasonCount; s++ {
				details.Seasons = append(details.Seasons, SeasonInfo{
					SeasonNumber: s,
					Name:         fmt.Sprintf("Season %d", s),
					EpisodeCount: 10,
				})
			}
		}
	}

	setWebCache(cacheKey, details, 30*time.Minute)
	writeJSON(w, details)
}

func (s *Server) handleTVEpisodes(w http.ResponseWriter, r *http.Request) {
	imdb := strings.TrimSpace(r.URL.Query().Get("imdb"))
	seasonStr := strings.TrimSpace(r.URL.Query().Get("season"))
	seasonNum, _ := strconv.Atoi(seasonStr)
	if seasonNum <= 0 {
		seasonNum = 1
	}

	cacheKey := fmt.Sprintf("episodes_v4_%s_%d", imdb, seasonNum)
	if data, ok := getWebCache(cacheKey); ok {
		writeJSON(w, data)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	var episodes []EpisodeInfo
	if imdb != "" {
		u := fmt.Sprintf("https://v3-cinemeta.strem.io/meta/series/%s.json", url.PathEscape(imdb))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err == nil {
			if resp, err := sharedClient.Do(req); err == nil {
				defer resp.Body.Close()
				var metaPayload struct {
					Meta struct {
						Videos []struct {
							ID          string `json:"id"`
							Title       string `json:"title"`
							Season      int    `json:"season"`
							Episode     int    `json:"episode"`
							Released    string `json:"released"`
							Description string `json:"description"`
							Thumbnail   string `json:"thumbnail"`
						} `json:"videos"`
					} `json:"meta"`
				}
				if json.NewDecoder(resp.Body).Decode(&metaPayload) == nil {
					for _, v := range metaPayload.Meta.Videos {
						if v.Season == seasonNum {
							name := v.Title
							if name == "" {
								name = fmt.Sprintf("Episode %d", v.Episode)
							}
							episodes = append(episodes, EpisodeInfo{
								EpisodeNumber: v.Episode,
								SeasonNumber:  v.Season,
								Name:          name,
								Overview:      v.Description,
								StillPath:     v.Thumbnail,
								AirDate:       v.Released,
								Runtime:       52,
								VoteAverage:   8.5,
							})
						}
					}
				}
			}
		}
	}

	if len(episodes) == 0 {
		epCount := 10
		for i := 1; i <= epCount; i++ {
			episodes = append(episodes, EpisodeInfo{
				EpisodeNumber: i,
				SeasonNumber:  seasonNum,
				Name:          fmt.Sprintf("Season %d • Episode %d", seasonNum, i),
				Overview:      "Stream high-speed 4K UHD video with pristine Dolby audio.",
				Runtime:       54,
				VoteAverage:   8.8,
			})
		}
	}

	setWebCache(cacheKey, episodes, 30*time.Minute)
	writeJSON(w, episodes)
}

func generateStreamingLinks(idStr, imdb, mType string, season, episode int) map[string]string {
	target := imdb
	if target == "" {
		target = idStr
	}
	if target == "" {
		target = "tt0816692" // Interstellar fallback
	}

	links := make(map[string]string)
	if mType == "tv" || mType == "series" || mType == "anime" {
		links["zen_ultra"] = fmt.Sprintf("https://player.vidlove.cc/embed/tv/%s/%d/%d", target, season, episode)
		links["zen_live"] = fmt.Sprintf("https://player.cinezo.live/embed/tv/%s/%d/%d", target, season, episode)
		links["zen_nitro"] = fmt.Sprintf("https://vidfast.vc/tv/%s/%d/%d", target, season, episode)
		links["zen_direct"] = fmt.Sprintf("https://vidbolt.xyz/tv/%s/%d/%d?theme=ffffff", target, season, episode)
		links["zen_cloud"] = fmt.Sprintf("https://embed.su/embed/tv/%s/%d/%d", target, season, episode)
		links["zen_edge"] = fmt.Sprintf("https://superembed.stream/tv/%s/%d/%d", target, season, episode)
	} else {
		links["zen_ultra"] = fmt.Sprintf("https://player.vidlove.cc/embed/movie/%s", target)
		links["zen_live"] = fmt.Sprintf("https://player.cinezo.live/embed/movie/%s", target)
		links["zen_nitro"] = fmt.Sprintf("https://vidfast.vc/movie/%s", target)
		links["zen_direct"] = fmt.Sprintf("https://vidbolt.xyz/movie/%s?theme=ffffff", target)
		links["zen_cloud"] = fmt.Sprintf("https://embed.su/embed/movie/%s", target)
		links["zen_edge"] = fmt.Sprintf("https://superembed.stream/movie/%s", target)
	}
	return links
}
