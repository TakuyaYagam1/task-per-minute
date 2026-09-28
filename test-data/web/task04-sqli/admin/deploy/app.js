const express = require('express');
const db = require('./db');
const app = express();
const PORT = 3004;

app.use(express.urlencoded({ extended: true }));

// Страница логина
app.get('/', (req, res) => {
    res.send(`
        <html>
        <head>
            <title>Вход в систему</title>
            <style>
                body {
                    background: #f0f0f0;
                    color: #333;
                    font-family: 'Tahoma', 'Arial', sans-serif;
                    margin: 0;
                    padding: 50px;
                    text-align: center;
                }
                .login-box {
                    background: #fff;
                    border: 2px solid #ccc;
                    padding: 30px;
                    width: 350px;
                    margin: 0 auto;
                    box-shadow: 3px 3px 10px rgba(0,0,0,0.1);
                }
                .login-box h1 {
                    color: #446688;
                    font-size: 20px;
                    margin-top: 0;
                    border-bottom: 1px solid #ddd;
                    padding-bottom: 15px;
                }
                .login-box input[type="text"],
                .login-box input[type="password"] {
                    width: 100%;
                    padding: 8px;
                    margin: 5px 0 15px 0;
                    border: 1px solid #ccc;
                    font-size: 14px;
                    box-sizing: border-box;
                }
                .login-box label {
                    display: block;
                    text-align: left;
                    font-size: 13px;
                    color: #666;
                }
                .login-box button {
                    background: #446688;
                    color: #fff;
                    border: none;
                    padding: 10px 30px;
                    font-size: 14px;
                    cursor: pointer;
                    margin-top: 10px;
                }
                .login-box button:hover {
                    background: #557799;
                }
                .footer {
                    margin-top: 20px;
                    color: #999;
                    font-size: 11px;
                }
            </style>
        </head>
        <body>
            <div class="login-box">
                <h1>Авторизация</h1>
                <form method="POST" action="/login">
                    <label>Имя пользователя:</label>
                    <input type="text" name="username" required>
                    <label>Пароль:</label>
                    <input type="password" name="password" required>
                    <button type="submit">Войти</button>
                </form>
            </div>
            <div class="footer">
                &copy; 2005 Панель управления
            </div>
        </body>
        </html>
    `);
});

// УЯЗВИМЫЙ эндпоинт - SQL Injection!
app.post('/login', (req, res) => {
    const { username, password } = req.body;

    // ВНИМАНИЕ: Уязвимый код! Никогда так не делайте в реальных проектах!
    const query = `SELECT * FROM users WHERE username = '${username}' AND password = '${password}'`;

    console.log('Executing query:', query);

    try {
        const user = db.prepare(query).get();

        if (user) {
            // Достаём флаг из таблицы flags
            const flag = db.prepare('SELECT flag FROM flags LIMIT 1').get();
            res.send(`
                <html>
                <head>
                    <title>Доступ разрешён</title>
                    <style>
                        body {
                            background: #f0f0f0;
                            color: #333;
                            font-family: 'Tahoma', 'Arial', sans-serif;
                            text-align: center;
                            padding: 80px;
                        }
                        .success {
                            background: #d4edda;
                            border: 2px solid #28a745;
                            padding: 30px;
                            display: inline-block;
                        }
                        .flag {
                            font-size: 20px;
                            color: #155724;
                            font-family: 'Courier New', monospace;
                            font-weight: bold;
                            margin-top: 20px;
                        }
                    </style>
                </head>
                <body>
                    <div class="success">
                        <h1>Добро пожаловать, ${user.username}!</h1>
                        <p>Вы успешно вошли в систему.</p>
                        <div class="flag">${flag.flag}</div>
                    </div>
                </body>
                </html>
            `);
        } else {
            res.send(`
                <html>
                <head>
                    <title>Ошибка входа</title>
                    <style>
                        body {
                            background: #f0f0f0;
                            color: #333;
                            font-family: 'Tahoma', 'Arial', sans-serif;
                            text-align: center;
                            padding: 80px;
                        }
                        .error {
                            background: #f8d7da;
                            border: 2px solid #dc3545;
                            padding: 30px;
                            display: inline-block;
                        }
                    </style>
                </head>
                <body>
                    <div class="error">
                        <h1>Ошибка входа</h1>
                        <p>Неверное имя пользователя или пароль.</p>
                    </div>
                </body>
                </html>
            `);
        }
    } catch (err) {
        res.send(`
            <html>
            <head>
                <title>Ошибка</title>
                <style>
                    body {
                        background: #f0f0f0;
                        color: #333;
                        font-family: 'Tahoma', 'Arial', sans-serif;
                        text-align: center;
                        padding: 80px;
                    }
                    .err-box {
                        background: #fff3cd;
                        border: 2px solid #ffc107;
                        padding: 30px;
                        display: inline-block;
                    }
                    pre {
                        background: #eee;
                        padding: 10px;
                        text-align: left;
                        font-size: 12px;
                    }
                </style>
            </head>
            <body>
                <div class="err-box">
                    <h1>Ошибка базы данных</h1>
                    <pre>${err.message}</pre>
                </div>
            </body>
            </html>
        `);
    }
});

app.listen(PORT, () => {
    console.log(`Task 4 (SQL Injection) running on http://localhost:${PORT}`);
});
