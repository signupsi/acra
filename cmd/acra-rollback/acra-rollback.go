// +build go1.8

// Copyright 2016, Cossack Labs Limited
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package main

import (
	"bufio"
	"container/list"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/cossacklabs/acra/cmd"
	"github.com/cossacklabs/acra/decryptor/base"
	"github.com/cossacklabs/acra/decryptor/postgresql"
	"github.com/cossacklabs/acra/keystore"
	"github.com/cossacklabs/acra/utils"
	"github.com/cossacklabs/themis/gothemis/keys"
	//_ "github.com/ziutek/mymysql/godrv"
	"github.com/cossacklabs/acra/keystore/filesystem"
	"github.com/cossacklabs/acra/logging"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	log "github.com/sirupsen/logrus"
)

// Constants used by AcraRollback
var (
	// DEFAULT_CONFIG_PATH relative path to config which will be parsed as default
	DEFAULT_CONFIG_PATH = utils.GetConfigPathByName("acra-rollback")
	SERVICE_NAME        = "acra-rollback"
)

// ErrorExit prints error and exits.
func ErrorExit(msg string, err error) {
	fmt.Println(utils.ErrorMessage(msg, err))
	os.Exit(1)
}

// BinaryEncoder encodes binary to string
type BinaryEncoder interface {
	Encode([]byte) string
}

// MysqlEncoder encodes MySQL packets
type MysqlEncoder struct{}

// Encode bytes to Hex supported by MySQL
func (e *MysqlEncoder) Encode(data []byte) string {
	return fmt.Sprintf("X'%s'", hex.EncodeToString(data))
}

// EscapeEncoder for Postgres
type EscapeEncoder struct{}

// Encode bytes to Postgres Octal format
func (e *EscapeEncoder) Encode(data []byte) string {
	return QuoteValue(string(postgresql.EncodeToOctal(data)))
}

// HexEncoder for hex
type HexEncoder struct{}

// Encode bytes to Hex
func (*HexEncoder) Encode(data []byte) string {
	return fmt.Sprintf("E'\\\\x%s'", hex.EncodeToString(data))
}

// Executor interface for any executor.
type Executor interface {
	Execute([]byte)
	Close()
}

// InsertExecutor will run Insert statement
type InsertExecutor struct {
	insertStatement *sql.Stmt
}

// NewInsertExecutor creates new executor for Insert statements
func NewInsertExecutor(sql string, db *sql.DB) *InsertExecutor {
	stmt, err := db.Prepare(sql)
	if err != nil {
		ErrorExit("can't prepare sql statement", err)
	}
	return &InsertExecutor{insertStatement: stmt}
}

// Execute inserts
func (ex *InsertExecutor) Execute(data []byte) {
	_, err := ex.insertStatement.Exec(&data)
	if err != nil {
		ErrorExit("can't bind args to prepared statement", err)
	}
}

// Close executor
func (ex *InsertExecutor) Close() {
	ex.insertStatement.Close()
}

// WriteToFileExecutor writes to file
type WriteToFileExecutor struct {
	encoder BinaryEncoder
	file    *os.File
	sql     string
	writer  *bufio.Writer
}

// NewWriteToFileExecutor creates new object ready to write encoded sql to filePath
func NewWriteToFileExecutor(filePath string, sql string, encoder BinaryEncoder) *WriteToFileExecutor {
	absPath, err := utils.AbsPath(filePath)
	if err != nil {
		ErrorExit("can't get absolute path for output file", err)
	}
	file, err := os.Create(absPath)
	if err != nil {
		ErrorExit("can't create output file", err)
	}
	writer := bufio.NewWriter(file)
	return &WriteToFileExecutor{sql: sql, file: file, writer: writer, encoder: encoder}
}

// PLACEHOLDER char
var PLACEHOLDER = "$1"

// NEWLINE char
var NEWLINE = []byte{'\n'}

// QuoteValue returns name in quotes, if name contains quotes, doubles them
func QuoteValue(name string) string {
	end := strings.IndexRune(name, 0)
	if end > -1 {
		name = name[:end]
	}
	return `'` + strings.Replace(name, `'`, `''`, -1) + `'`
}

// Execute write to file
func (ex *WriteToFileExecutor) Execute(data []byte) {
	encoded := ex.encoder.Encode(data)
	outputSQL := strings.Replace(ex.sql, PLACEHOLDER, encoded, 1)
	n, err := ex.writer.Write([]byte(outputSQL))
	if err != nil {
		ErrorExit("can't write to output file", err)
	}
	if n != len(outputSQL) {
		fmt.Println("Incorrect write count")
		os.Exit(1)
	}
	n, err = ex.writer.Write(NEWLINE)
	if err != nil {
		ErrorExit("can't write newline char to output file", err)
	}
	if n != 1 {
		fmt.Println("Incorrect write count")
		os.Exit(1)
	}
}

// Close file
func (ex *WriteToFileExecutor) Close() {
	ex.writer.Flush()
	ex.file.Sync()
	ex.file.Close()
}

func main() {
	keysDir := flag.String("keys_dir", keystore.DEFAULT_KEY_DIR_SHORT, "Folder from which the keys will be loaded")
	clientID := flag.String("client_id", "", "Client ID should be name of file with private key")
	connectionString := flag.String("connection_string", "", "Connection string for db")
	sqlSelect := flag.String("select", "", "Query to fetch data for decryption")
	sqlInsert := flag.String("insert", "", "Query for insert decrypted data with placeholders (pg: $n, mysql: ?)")
	withZone := flag.Bool("zonemode_enable", false, "Turn on zone mode")
	outputFile := flag.String("output_file", "decrypted.sql", "File for store inserts queries")
	execute := flag.Bool("execute", false, "Execute inserts")
	escapeFormat := flag.Bool("escape", false, "Escape bytea format")
	useMysql := flag.Bool("mysql_enable", false, "Handle MySQL connections")
	usePostgresql := flag.Bool("postgresql_enable", false, "Handle Postgresql connections")

	logging.SetLogLevel(logging.LOG_VERBOSE)

	err := cmd.Parse(DEFAULT_CONFIG_PATH, SERVICE_NAME)
	if err != nil {
		log.WithError(err).Errorln("can't parse args")
		os.Exit(1)
	}

	twoDrivers := *useMysql && *usePostgresql
	noDrivers := !(*useMysql || *usePostgresql)
	if twoDrivers || noDrivers {
		log.Errorln("you must pass only --mysql_enable or --postgresql_enable (one required)")
		os.Exit(1)
	}
	if *useMysql {
		PLACEHOLDER = "?"
	}

	if !strings.Contains(*sqlInsert, PLACEHOLDER) {
		log.Errorln("SQL INSERT statement doesn't contain any placeholders")
		os.Exit(1)
	}

	dbDriverName := "postgres"
	if *useMysql {
		// https://github.com/ziutek/mymysql
		//dbDriverName = "mymysql"
		// https://github.com/go-sql-driver/mysql/
		dbDriverName = "mysql"
	}

	cmd.ValidateClientID(*clientID)

	if *connectionString == "" {
		log.Errorln("connection_string arg is missing")
		os.Exit(1)
	}

	if *sqlSelect == "" {
		log.Errorln("sql_select arg is missing")
		os.Exit(1)
	}
	if *sqlInsert == "" {
		log.Errorln("sql_insert arg is missing")
		os.Exit(1)
	}
	absKeysDir, err := utils.AbsPath(*keysDir)
	if err != nil {
		log.WithError(err).Errorln("can't get absolute path for keys_dir")
		os.Exit(1)
	}
	if *outputFile == "" && !*execute {
		log.Errorln("output_file missing or execute flag")
		os.Exit(1)
	}
	masterKey, err := keystore.GetMasterKeyFromEnvironment()
	if err != nil {
		log.WithError(err).Errorln("can't load master key")
		os.Exit(1)
	}
	scellEncryptor, err := keystore.NewSCellKeyEncryptor(masterKey)
	if err != nil {
		log.WithError(err).Errorln("can't init scell encryptor")
		os.Exit(1)
	}
	keystorage, err := filesystem.NewFilesystemKeyStore(absKeysDir, scellEncryptor)
	if err != nil {
		log.WithError(err).Errorln("can't create key store")
		os.Exit(1)
	}
	db, err := sql.Open(dbDriverName, *connectionString)
	if err != nil {
		log.WithError(err).Errorln("can't connect to db")
		os.Exit(1)
	}
	defer db.Close()
	err = db.Ping()
	if err != nil {
		log.WithError(err).Errorln("can't connect to db")
		os.Exit(1)
	}
	rows, err := db.Query(*sqlSelect)
	if err != nil {
		log.WithError(err).Errorf("error with select query '%v'", *sqlSelect)
		os.Exit(1)
	}
	defer rows.Close()

	executors := list.New()
	if *outputFile != "" {
		if *useMysql {
			executors.PushFront(NewWriteToFileExecutor(*outputFile, *sqlInsert, &MysqlEncoder{}))
		} else {
			if *escapeFormat {
				executors.PushFront(NewWriteToFileExecutor(*outputFile, *sqlInsert, &EscapeEncoder{}))
			} else {
				executors.PushFront(NewWriteToFileExecutor(*outputFile, *sqlInsert, &HexEncoder{}))
			}
		}
	}
	if *execute {
		executors.PushFront(NewInsertExecutor(*sqlInsert, db))
	}
	for e := executors.Front(); e != nil; e = e.Next() {
		executor := e.Value.(Executor)
		defer executor.Close()
	}

	var data, zone []byte
	var privateKey *keys.PrivateKey

	for i := 0; rows.Next(); i++ {
		if *withZone {
			err = rows.Scan(&zone, &data)
			if err != nil {
				ErrorExit("can't read zone & data from row %v", err)
			}
			privateKey, err = keystorage.GetZonePrivateKey(zone)
			if err != nil {
				log.WithError(err).Errorf("can't get zone private key for row with number %v", i)
				continue
			}
		} else {
			err = rows.Scan(&data)
			if err != nil {
				ErrorExit("can't read data from row", err)
			}
			privateKey, err = keystorage.GetServerDecryptionPrivateKey([]byte(*clientID))
			if err != nil {
				log.WithError(err).Errorf("can't get private key for row with number %v", i)
				continue
			}
		}
		decrypted, err := base.DecryptAcrastruct(data, privateKey, zone)
		if err != nil {
			log.WithError(err).Errorln("can't decrypt acrastruct in row with number %v", i)
			continue
		}
		for e := executors.Front(); e != nil; e = e.Next() {
			executor := e.Value.(Executor)
			executor.Execute(decrypted)
		}
	}
}