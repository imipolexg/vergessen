package deck

import (
	"database/sql"
	"errors"
	_ "github.com/mattn/go-sqlite3"
	"os"
	"time"
)

type Deck struct {
	Path  string
	DB    *sql.DB
	Cards []*Card
	Dirty bool
}

type Card struct {
	Id      int
	Prompt  string
	Answer  string
	Reps    int
	NextRep time.Time

	// These two slices are indexed by the rep - 1
	EFs        []float64
	Hardnesses []int
}

var defaultEF float64 = 1.75
var createDeckStmt string = `
create table cards (id integer not null primary key, prompt text, answer text, reps integer, nextrep integer);
create table efs (id integer not null primary key, card_id integer not null, ef float64);
create table hardnesses (id integer not null primary key, card_id integer not null, hardness integer);
`

func OpenDeck(path string) (*Deck, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}

	deck := &Deck{
		path,
		db,
		make([]*Card, 0, 1),
		false,
	}

	cardRows, err := db.Query("select * from cards order by nextrep")
	if err != nil {
		// There must be a better way to check if the table exists or not?
		if err.Error() == "no such table: cards" {
			_, err = db.Exec(createDeckStmt)
			if err != nil {
				return nil, err
			}

			return deck, nil
		}

		// Deck exists, but empty
		if err == sql.ErrNoRows {
			return deck, nil
		}

		// Some other error
		return nil, err
	}
	defer cardRows.Close()

	for cardRows.Next() {
		var id int
		var prompt string
		var answer string
		var reps int
		var nextrep int64

		if err := cardRows.Scan(&id, &prompt, &answer, &reps, &nextrep); err != nil {
			return nil, err
		}

		card := NewCard(prompt, answer)
		card.Id = id
		card.Reps = reps
		card.NextRep = time.Unix(nextrep, 0)

		efRows, err := db.Query("select ef from efs where card_id = $1 order by id", id)
		if err == sql.ErrNoRows && card.Reps > 0 {
			return nil, errors.New("no easiness factors for this card!")
		}
		defer efRows.Close()

		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}

		for efRows.Next() {
			var ef float64
			if err := efRows.Scan(&ef); err != nil {
				return nil, err
			}

			card.EFs = append(card.EFs, ef)
		}

		hardnessRows, err := db.Query("select hardness from hardnesses where card_id = $1 order by id", id)
		if err == sql.ErrNoRows && card.Reps > 0 {
			return nil, errors.New("no hardness factors for this card!")
		}
		defer hardnessRows.Close()

		for hardnessRows.Next() {
			var hardness int
			if err := hardnessRows.Scan(&hardness); err != nil {
				return nil, err
			}

			card.Hardnesses = append(card.Hardnesses, hardness)
		}

		deck.AddCard(card)
	}

	deck.Dirty = false
	return deck, nil
}

func (d *Deck) AddCard(card *Card) {
	if len(d.Cards) > 0 {
		card.Id = d.Cards[len(d.Cards)-1].Id + 1
	} else {
		card.Id = 0
	}

	d.Cards = append(d.Cards, card)
	d.Dirty = true
}

func (d *Deck) DeleteCard(id int) {
	for i, card := range d.Cards {
		if card.Id == id {
			d.Cards = append(d.Cards[:i], d.Cards[i+1:]...)
			d.Dirty = true
			return
		}
	}
}

// Write the current deck disk. Uses a pretty naive method, writing the whole
// deck to a temporary file, then copying that file over the original one
func (d *Deck) Sync() error {
	// check if file exists first, and if so use another name
	new_path := d.Path + ".sync"
	db, err := sql.Open("sqlite3", new_path)
	if err != nil {
		return err
	}

	_, err = db.Exec(createDeckStmt)
	if err != nil {
		return err
	}

	insertCardStmt := "insert into cards (prompt, answer, reps, nextrep) values ($1, $2, $3, $4)"
	insertEFStmt := "insert into efs (card_id, ef) values ($1, $2)"
	insertHardnessStmt := "insert into hardnesses (card_id, hardness) values ($1, $2)"

	for _, card := range d.Cards {
		nextrep := card.NextRep.Unix()
		res, err := db.Exec(insertCardStmt, card.Prompt, card.Answer, card.Reps, nextrep)
		if err != nil {
			return err
		}

		id, err := res.LastInsertId()
		if err != nil {
			return err
		}

		for _, ef := range card.EFs {
			_, err := db.Exec(insertEFStmt, id, ef)
			if err != nil {
				return err
			}
		}

		for _, hardness := range card.Hardnesses {
			_, err := db.Exec(insertHardnessStmt, id, hardness)
			if err != nil {
				return err
			}
		}
	}

	db.Close()
	// XXX: Have a better strategy here for when errors occur
	err = os.Remove(d.Path)
	if err != nil {
		return err
	}

	err = os.Rename(new_path, d.Path)
	if err != nil {
		return err
	}

	d.DB, err = sql.Open("sqlite3", d.Path)
	if err != nil {
		return err
	}

	d.Dirty = false
	return nil
}

func (d *Deck) Close() error {
	if d.Dirty {
		if err := d.Sync(); err != nil {
			d.DB.Close()
			return err
		}
	}
	d.DB.Close()
	return nil
}

func NewCard(prompt, answer string) *Card {
	return &Card{
		0,
		prompt,
		answer,
		0,
		time.Now(),
		make([]float64, 0, 1),
		make([]int, 0, 1),
	}
}

func (c *Card) CalcNextRep(hardness int) {
	c.Reps++

	c.Hardnesses = append(c.Hardnesses, hardness)

	if c.Reps == 1 {
		c.NextRep = time.Now().Add(time.Hour * 24)
		c.EFs = append(c.EFs, defaultEF)
		return
	} else if c.Reps == 2 {
		// SM-2 specifies 6 days, but let's do 4.
		// XXX: Make it configurable later
		c.NextRep = time.Now().Add(time.Hour * 24 * 4)
		c.EFs = append(c.EFs, defaultEF)
		return
	}

	c.EFs = append(c.EFs, calcEf(c.EFs[c.Reps-2], c.Hardnesses[c.Reps-1]))
	days := c.interval(c.Reps)
	c.NextRep = time.Now().Add((time.Duration)(float64(time.Hour) * 24 * days))
}

func (c *Card) interval(n int) float64 {
	if n == 1 {
		return 1.0
	} else if n == 2 {
		return 6.0
	}

	return c.interval(n-1) * c.EFs[n-1]
}

func calcEf(efprime float64, hardness int) float64 {
	ef := efprime - 0.8 + 0.28*float64(hardness) - 0.02*float64(hardness*hardness)

	if ef < 1.3 {
		return 1.3
	}

	return ef
}
