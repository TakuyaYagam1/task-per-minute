const Database = require('better-sqlite3');
const path = require('path');

const dbPath = process.env.DB_PATH || path.join(__dirname, 'ctf.db');
const db = new Database(dbPath);

// Создаём таблицы и заполняем данными
db.exec(`
    CREATE TABLE IF NOT EXISTS users (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        username TEXT NOT NULL,
        password TEXT NOT NULL
    );

    CREATE TABLE IF NOT EXISTS flags (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        flag TEXT NOT NULL
    );

    INSERT OR IGNORE INTO users (id, username, password) VALUES (1, 'admin', 'supersecretpassword123');
    INSERT OR IGNORE INTO flags (id, flag) VALUES (1, 'flag{sql_1nj3ct10n_1s_34sy}');
`);

module.exports = db;
