package main

// Vergessen
//
// A simple implementation of the SM-2 algorithm, developed by P.A. Wozniak
//
// See https://www.supermemo.com/english/ol/sm2.htm
//
// Cards are pairs of prompts and answers.
// The prompt is displayed, and the user has to come up with the answer. Then
// the program shows the answer. The user then indicates how difficult it was
// for them to come up with the answer based on the prompt. The selection of a
// difficulty level determines the amount of time the program should wait before
// showing the prompt again.
//
// The interval determination is fairly simple. It is determined by a function,
// I(n) that calculates the number of days to delay a card. n is the number of
// times that the user has seen the prompt/answer pair, and I is defined
// as follows:
//
// If n = 1, I(n) = 1
// If n = 2, I(n) = 6
// If n > 2, I(n) = I(n-1) * EF
//
// EF, the most complicated part of SM-2, is the 'easiness factor'.
//
// EF is determined by the following recursive function:
//
// EF = f(EF', q)
//
// Where q is the quality rating the user provides (between 5 and 0), EF'
// is the previous EF, or 2.5 if this is the first time n > 2, and where f is:
//
// EF = f(EF', q) = EF' - 0.8 + 0.28 * q - 0.02 * q * q
//
// So, for n == 3, with q (hardness) of 3, we calculate like so:
//
// I(n = 3) = I(2) * 2.5 - 0.8 + 0.28 * 3 - 0.02 * 3 * 3
//  or
// I(3) = 6 * 2.5 - 0.8 * 3 - 0.02 * 3 * 3 = 14.16
//
// For n = 4, we calculate:
//
// I(4) = I(3) * 2.5 - 0.8 etc., which means we have to expand the calculation
// for all the preceding intervals
//
// NOTE: The default EF has been changed to 1.75 instead of 2.5 and will be
// NOTE: user configurable

import (
	"bufio"
	"database/sql"
	"errors"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"io"
	"io/ioutil"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
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

var maxStudy = 20

var dbVersion = 1
var defaultEF float64 = 1.75
var defaultHardness = 2

var createDeckStmt string = `
create table cards (id integer not null primary key, prompt text, answer text, reps integer, nextrep integer);
create table efs (id integer not null primary key, card_id integer not null, ef float64);
create table hardnesses (id integer not null primary key, card_id integer not null, hardness integer);
`

// This is a hack to work around the initialization loop caused by showHelp's
// reference to cmds
func init() {
	cmds["?"] = Command{showHelp, "show this help"}
}

type Command struct {
	Callback func(*Deck, []string) error
	Help     string
}

var cmds map[string]Command = map[string]Command{
	"study": {study, "study all due cards."},
	"list":  {list, "list all cards in the deck."},
	"quit":  {quit, "quit"},
	"new":   {newCard, "create a new card"},
	"del":   {delCard, "delete a card by id"},
	"edit":  {editCard, "edit a card"},
	"due":   {dueCards, "see the cards due"},
}

var quitError error = errors.New("Peace!")

func main() {
	if len(os.Args[1:]) < 1 {
		fmt.Fprintln(os.Stderr, "No deck specified")
		os.Exit(1)
	}

	d, err := OpenDeck(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
	}
	defer d.Close()

	fmt.Println("Opened deck", os.Args[1])
	fmt.Println(len(d.Cards), "cards. Enter ? for help.")

	rdr := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("nichtvergessen> ")
		input, err := rdr.ReadString('\n')
		if err != nil {
			return
		}

		input = strings.Trim(input, " \n\t\r")
		if input == "" {
			continue
		}

		tokens := strings.Split(input, " ")
		cmd, ok := cmds[tokens[0]]
		if !ok {
			fmt.Println("Don't know what to do with '", tokens[0], "'. Enter '?' for help")
			continue
		}

		err = cmd.Callback(d, tokens[1:])
		if err != nil && err != quitError {
			fmt.Println("Bad news bears:", err)
		}

		if err == quitError {
			fmt.Println("Ill will rest in peace yo' I'm out")
			return
		}
	}
}

func showHelp(d *Deck, args []string) error {
	fmt.Println("The following commands are available\n")

	cmdnames := make([]string, len(cmds))
	i := 0
	for name := range cmds {
		cmdnames[i] = name
		i++
	}
	sort.Sort(sort.StringSlice(cmdnames))

	tabwrt := new(tabwriter.Writer)
	tabwrt.Init(os.Stdout, 0, 8, 0, '\t', 0)
	for _, name := range cmdnames {
		fmt.Fprintf(tabwrt, " %s\t%s\n", name, cmds[name].Help)
	}
	tabwrt.Flush()
	fmt.Println()

	return nil
}

func dueCards(d *Deck, args []string) error {
	due := 0
	now := time.Now().Unix()
	for _, card := range d.Cards {
		if card.NextRep.Unix() < now {
			due++
		}
	}

	fmt.Println(due, "cards due.")
	return nil
}

func study(d *Deck, args []string) error {
	var studied = 0
	for _, card := range d.Cards {
		now := time.Now()

		if now.Unix() < card.NextRep.Unix() {
			continue
		}

		fmt.Println(card.Prompt)
		_, err := getInput("Press ENTER to see the ANSWER")
		if err != nil {
			return err
		}

		var hardness int
		for {
			fmt.Println(card.Answer)
			hardStr, err := getInput("Enter HARDNESS (1-5) and hit ENTER> ")
			if err != nil {
				return err
			}

			// If the user just hits enter, make hardness == 2
			if hardStr == "\n" {
				hardness = defaultHardness
				break
			} else {
				hardness, err = strconv.Atoi(strings.Trim(hardStr, " \n\t\r"))
				if err == nil {
					break
				}

				fmt.Printf("Error reading hardness: %v\n", err)
			}
		}

		card.CalcNextRep(hardness)
		d.Dirty = true

		studied++
		if studied > maxStudy {
			break
		}
	}

	err := d.Sync()
	return err
}

func quit(d *Deck, args []string) error {
	return quitError
}

func list(d *Deck, args []string) error {
	tabwrt := new(tabwriter.Writer)
	tabwrt.Init(os.Stdout, 0, 8, 1, '\t', 0)

	fmt.Fprintln(tabwrt, "Id\tReps\tDue\tPrompt")

	div := strings.Repeat("-----\t", 4)
	div = div[:len(div)-1]
	fmt.Fprintln(tabwrt, div)

	for _, card := range d.Cards {
		fmt.Fprintf(tabwrt, "%d\t%d", card.Id, card.Reps)

		due := fmtDue(card.NextRep.Unix())
		fmt.Fprintf(tabwrt, "\t%s", due)

		var prompt string
		displayLen := 32
		if len(card.Prompt) < displayLen {
			prompt = card.Prompt
		} else {
			prompt = card.Prompt[:displayLen] + "..."
		}

		// Use a regex to do this in a single pass?
		prompt = strings.Replace(prompt, "\n", " ", -1)
		prompt = strings.Replace(prompt, "\t", " ", -1)

		fmt.Fprintf(tabwrt, "\t%s\n", prompt)
	}
	tabwrt.Flush()

	return nil
}

func fmtDue(dueTime int64) string {
	now := time.Now().Unix()
	until := dueTime - now

	if until < 0 {
		return "now"
	}

	days := int(until / (24 * 60 * 60))
	switch {
	case days > 0 && days < 7:
		return fmt.Sprintf("%d days", days)
	case days >= 7 && days <= 365:
		weeks := days / 7
		return fmt.Sprintf("%d weeks", weeks)
	default:
		months := days / 30
		return fmt.Sprintf("%d months", months)
	}
}

func cardNumberFromArgs(args []string) (int, error) {
	if len(args) < 1 {
		return 0, errors.New("No card # given")
	}

	num, err := strconv.Atoi(args[0])
	if err != nil {
		return 0, err
	}

	return num, nil
}

func delCard(d *Deck, args []string) error {
	id, err := cardNumberFromArgs(args)
	if err != nil {
		return err
	}
	d.DeleteCard(id)

	return nil
}

func editCard(d *Deck, args []string) error {
	id, err := cardNumberFromArgs(args)
	if err != nil {
		return err
	}

	var c *Card = nil
	for _, card := range d.Cards {
		if card.Id == id {
			c = card
			break
		}
	}
	if c == nil {
		return errors.New(fmt.Sprintf("Unknown card id = %d", id))
	}

	_, err = getInput("Press ENTER to edit the PROMPT, or CTRL+D to leave as is")
	if err == nil {
		newPrompt, err := spawnEditor("prompt", c.Prompt)
		if err != nil {
			return err
		}
		c.Prompt = newPrompt
	} else if err != nil && err != io.EOF {
		return err
	} else {
		fmt.Print("\n")
	}

	_, err = getInput("Press ENTER to edit the ANSWER, or CTRL+D to leave as is")
	if err == nil {
		newAnswer, err := spawnEditor("answer", c.Answer)
		if err != nil {
			return err
		}
		c.Answer = newAnswer
	} else if err != nil && err != io.EOF {
		return err
	} else {
		fmt.Print("\n")
	}

	return nil
}

func spawnEditor(prefix, contents string) (string, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		return "", errors.New("Set your EDITOR env variable!")
	}

	tmpFile, err := ioutil.TempFile("/tmp", fmt.Sprintf("vergessen.%s.", prefix))
	if err != nil {
		return "", err
	}

	if len(contents) > 0 {
		if _, err := tmpFile.WriteString(contents); err != nil {
			return "", err
		}
	}
	tmpFile.Close()

	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	cmdArgs := []string{"/bin/sh", "-c", fmt.Sprintf("%s %s", editor, tmpPath)}
	fmt.Println(cmdArgs)
	procAttr := os.ProcAttr{
		"",
		nil,
		[]*os.File{
			os.Stdin,
			os.Stdout,
			os.Stderr,
		},
		nil,
	}

	proc, err := os.StartProcess("/bin/sh", cmdArgs, &procAttr)
	if err != nil {
		return "", err
	}
	_, err = proc.Wait()
	if err != nil {
		return "", err
	}

	result, err := ioutil.ReadFile(tmpPath)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

func getInput(prompt string) (string, error) {
	rdr := bufio.NewReader(os.Stdin)
	fmt.Print(prompt)
	input, err := rdr.ReadString('\n')
	if err != nil {
		return "", err
	}
	return input, nil
}

func newCard(d *Deck, args []string) error {
	_, err := getInput("Press ENTER to edit the card PROMPT")
	if err != nil {
		return err
	}

	prompt, err := spawnEditor("prompt", "Write the PROMPT here and save+quit")
	if err != nil {
		return err
	}
	fmt.Println("Prompt:", prompt)

	_, err = getInput("Press ENTER to edit the card ANSWER")
	if err != nil {
		return err
	}
	answer, err := spawnEditor("answer", "Write the ANSWER here and save+quit")
	if err != nil {
		return err
	}

	fmt.Println("Answer:", answer)

	card := NewCard(prompt, answer)
	d.AddCard(card)

	return nil
}

func OpenDeck(path string) (*Deck, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}

	d := &Deck{
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

			return d, nil
		}

		// Deck exists, but empty
		if err == sql.ErrNoRows {
			return d, nil
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

		d.AddCard(card)
	}

	d.Dirty = false
	return d, nil
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
	if hardness == 5 {
		c.NextRep = time.Now()
	} else {
		days := c.interval(c.Reps)
		c.NextRep = time.Now().Add((time.Duration)(float64(time.Hour) * 24 * days))
	}
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
