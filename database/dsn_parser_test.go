// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package database

import (
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
)

func TestPostgresConfig_DSN_ParsesWithPGX(t *testing.T) {
	cfg := PostgresConfig{
		Host:     "::1",
		Port:     5432,
		User:     "user",
		Password: "p@ss:w/rd#?",
		Name:     "mydb",
		SSLMode:  "disable",
	}

	parsed, err := pgx.ParseConfig(cfg.DSN())
	if err != nil {
		t.Fatalf("pgx.ParseConfig returned unexpected error: %v", err)
	}

	if parsed.Host != "::1" {
		t.Errorf("Host = %q, want %q", parsed.Host, "::1")
	}
	if parsed.Port != 5432 {
		t.Errorf("Port = %d, want %d", parsed.Port, 5432)
	}
	if parsed.User != "user" {
		t.Errorf("User = %q, want %q", parsed.User, "user")
	}
	if parsed.Password != "p@ss:w/rd#?" {
		t.Errorf("Password = %q, want %q", parsed.Password, "p@ss:w/rd#?")
	}
	if parsed.Database != "mydb" {
		t.Errorf("Database = %q, want %q", parsed.Database, "mydb")
	}
	if got := parsed.RuntimeParams["sslmode"]; got != "disable" {
		t.Errorf("sslmode = %q, want %q", got, "disable")
	}
}

func TestMySQLConfig_DSN_ParsesWithDriver(t *testing.T) {
	cfg := MySQLConfig{
		Host:     "::1",
		Port:     3306,
		User:     "user",
		Password: "p@ss:w/rd#?",
		Name:     "mydb",
	}

	parsed, err := mysql.ParseDSN(cfg.DSN())
	if err != nil {
		t.Fatalf("mysql.ParseDSN returned unexpected error: %v", err)
	}

	if parsed.User != "user" {
		t.Errorf("User = %q, want %q", parsed.User, "user")
	}
	if parsed.Passwd != "p@ss:w/rd#?" {
		t.Errorf("Passwd = %q, want %q", parsed.Passwd, "p@ss:w/rd#?")
	}
	if parsed.Net != "tcp" {
		t.Errorf("Net = %q, want %q", parsed.Net, "tcp")
	}
	if parsed.Addr != "[::1]:3306" {
		t.Errorf("Addr = %q, want %q", parsed.Addr, "[::1]:3306")
	}
	if parsed.DBName != "mydb" {
		t.Errorf("DBName = %q, want %q", parsed.DBName, "mydb")
	}
	if !parsed.ParseTime {
		t.Fatal("ParseTime = false, want true")
	}
}
