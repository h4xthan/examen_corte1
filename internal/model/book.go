package model

type Book struct {
	// ID crosses the wire as a string, and that is not cosmetic.
	//
	// TiDB assigns book ids with AUTO_RANDOM, so they are 19-digit integers
	// like 4035225266124144421. A JSON number of that size is exact in Go and
	// inexact the instant JavaScript's JSON.parse turns it into a float64: the
	// largest integer a double holds exactly is 2^53, and 4035225266124144421
	// is eight orders of magnitude past it. The browser then sent the rounded
	// value back, and the panel edited or deleted nothing while reporting
	// success. Every identifier in this package is therefore quoted on the way
	// out, and the request DTOs that take an identifier accept it quoted.
	ID     int64  `json:"id,string"`
	Author string `json:"author"`
	Title  string `json:"title"`
	Pages  int    `json:"pages"`
	ISBN   string `json:"isbn"`
	// PriceCents is an integer number of cents, never a float.
	//
	// The field was `price` as a NUMERIC(10,2) that Go decoded into a float64,
	// and checkout multiplied it by a client-supplied quantity. Binary floating
	// point cannot represent most decimal fractions exactly, so 19.99 came back
	// as 19.989999999999998 and the error was carried into the order total.
	PriceCents    int64  `json:"price_cents"`
	Stock         int    `json:"stock"`
	URLCoverImage string `json:"url_cover_image"`
}
