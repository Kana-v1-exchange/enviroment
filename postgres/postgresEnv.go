package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v4"
)

type PostgreSettings struct {
	User     string
	Password string
	Host     string
	Port     string
	DbName   string
}

type PostgresHandler interface {
	GetCurrencies() (map[string]float64, error)
	GetUsersNum() (int, error)
	UpdateCurrency(currency string, value float64) error
	GetCurrencyAmount(currency string) (float64, error)
	GetCurrencyValue(currency string) (float64, error)
	UpdateCurrencyAmount(userID uint64, currency string, value float64) error
	AddUser(email, password string) error
	GetUserData(email string) (uint64, string, error)
	SendCurrency(sellerID, buyerID uint64, currency string, value float64) error
	FindSeller(currency string, value float64) (uint64, error)
}

type postgresClient struct {
	connection *pgx.Conn
}

func (ps *PostgreSettings) Connect() PostgresHandler {
	connStr := fmt.Sprintf("postgresql://%s:%s@%s/%s", ps.User, ps.Password, ps.Host, ps.DbName)

	conn, err := pgx.Connect(context.Background(), connStr)
	if err != nil {
		panic(fmt.Errorf("cannot connect to the postgres database; err: %v", err))
	}

	err = conn.Ping(context.Background())
	if err != nil {
		panic(fmt.Errorf("cannot ping the postgres database; error: %v", err))
	}

	return &postgresClient{conn}
}

func (pc *postgresClient) GetCurrencies() (map[string]float64, error) {
	res := make(map[string]float64)

	rows, err := pc.connection.Query(context.Background(), "SELECT * FROM currencies")
	if err != nil {
		return nil, fmt.Errorf("cannot get currencies from the postgres database; err: %v", err)
	}

	for rows.Next() {
		var currency string
		var value float64
		err = rows.Scan(&currency, &value)

		if err != nil {
			return nil, fmt.Errorf("cannot scan value from the postgres database; err: %v", err)
		}

		res[currency] = value
	}

	return res, nil
}

func (pc *postgresClient) UpdateCurrency(currency string, value float64) error {
	_, err := pc.connection.Exec(context.Background(),
		`UPDATE currencies
		 SET value = $1`,
		value)

	if err != nil {
		return fmt.Errorf("postgres can not update currency %v to the new value %v; err: %v", currency, value, err)
	}

	return nil
}

func (pc *postgresClient) GetUsersNum() (int, error) {
	res := 0
	err := pc.connection.QueryRow(context.Background(), "SELECT COUNT(id) FROM users").Scan(&res)

	if err != nil && err != sql.ErrNoRows {
		return 0, fmt.Errorf("cann get number of users from the postgres database; error: %v", err)
	}

	return res, nil
}

func (pc *postgresClient) GetCurrencyAmount(currency string) (float64, error) {
	amount := float64(0)
	err := pc.connection.QueryRow(
		context.Background(),
		`SELECT SUM(amount)
		 FROM users_money
		 WHERE currency = $1`,
		currency,
	).Scan(&amount)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}

		return 0, fmt.Errorf("postgres cannot return amount of the currency %v; err: %v", currency, err)
	}

	return amount, nil
}

func (pc *postgresClient) GetCurrencyValue(currency string) (float64, error) {
	row := pc.connection.QueryRow(
		context.Background(),
		`SELECT amount 
		 FROM currencies 
		 WHERE currency = $1`,
		currency,
	)

	value := float64(0)
	err := row.Scan(&value)
	if err != nil {
		return 0, fmt.Errorf("cannot get currencies'(%v) value; err: %v", currency, err)
	}

	return value, nil
}

func (pc *postgresClient) UpdateCurrencyAmount(userID uint64, currency string, value float64) error {
	_, err := pc.connection.Exec(
		context.Background(),
		`UPDATE users_money
		 SET amount = $1
		 WHERE user_id = $2
		 AND currency = $3`,
		value,
		userID,
		currency,
	)

	if err != nil {
		return fmt.Errorf("cannot update user's (id = %v) currency (%v); err: %v", userID, currency, err)
	}

	return nil
}

func (pc *postgresClient) AddUser(email, password string) error {
	_, err := pc.connection.Exec(
		context.Background(),
		`INSERT INTO users (email, password)
		 VALUES($1, $2)`,
		email,
		password,
	)

	if err != nil {
		return fmt.Errorf("cannot update user's (email: %v, password: %v) data; err: %v", email, password, err)
	}

	return nil
}

func (pc *postgresClient) GetUserData(email string) (uint64, string, error) {
	id := uint64(0)
	password := ""

	row := pc.connection.QueryRow(
		context.Background(),
		`SELECT id, password 
		 FROM users 
		 WHERE email = $1`,
		email,
	)

	err := row.Scan(&id, password)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, "", err
		}

		return 0, "", fmt.Errorf("postgres cannot return user's data (email = %v); err: %v", email, err)
	}

	return id, email, nil
}

func (pc *postgresClient) FindSeller(currency string, value float64) (uint64, error) {
	sellerID := uint64(0)
	rows := pc.connection.QueryRow(
		context.Background(),
		`SELECT user_id 
		 FROM users_money 
		 WHERE currency = $1
		 AND amount >= $2`,
		currency,
		value,
	)

	err := rows.Scan(&sellerID)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("nobody has %v %v", value, currency)
		}

		return 0, err
	}

	return sellerID, nil
}

func (pc *postgresClient) SendCurrency(sellerID, buyerID uint64, currency string, value float64) error {
	tx, err := pc.connection.Begin(context.Background())

	if err != nil {
		return fmt.Errorf("cannot start transaction; err %v", err)
	}

	amount := float64(0)

	rows, err := tx.Query(
		context.Background(),
		`SELECT amount 
		 FROM users_money 
		 WHERE currency = $1
		 AND user_id = $3
		 LIMIT 1`,
		currency,
		sellerID,
	)

	if err != nil {
		tx.Rollback(context.Background())

		if err == sql.ErrNoRows {
			return fmt.Errorf("%w; user with id %v does not have %v %v", sql.ErrNoRows, sellerID, value, currency)
		}

		return fmt.Errorf("cannot get %v %v from the users_money table; err: %v", value, currency, err)
	}

	for rows.Next() {
		rows.Scan(&sellerID, amount)
	}

	_, err = tx.Exec(
		context.Background(),
		`UPDATE users_money
		 SET amount = $1
		 WHERE user_id = $2
		 AND currency = $3`,
		amount-value,
		sellerID,
		currency,
	)

	if err != nil {
		tx.Rollback(context.Background())
		return fmt.Errorf("cannot sell user's (id = %v) currency(%s); err: %v", sellerID, currency, err)
	}

	_, err = tx.Exec(
		context.Background(),
		`UPDATE users_money
		 SET amount = (
			SELECT amount 
			FROM users_money 
			WHERE user_id = $1
			AND currency = $2
		 ) + $3
		 WHERE user_id = $1`,
		buyerID,
		currency,
		value,
	)

	if err != nil {
		tx.Rollback(context.Background())
		return fmt.Errorf("cannot update currency amount; err: %v", err)
	}

	err = tx.Commit(context.Background())
	if err != nil {
		return fmt.Errorf("cannot rollback transaction; err: %v", err)
	}

	return nil
}
