const express = require('express');
const app = express();
const PORT = 3005;

app.use(express.urlencoded({ extended: true }));

app.get('/flag', (req, res) => {
    res.status(404).send(`
        <html>
        <head>
            <title>404</title>
            <style>
                body {
                    background: #e8f4f8;
                    color: #333;
                    font-family: 'Tahoma', 'Arial', sans-serif;
                    text-align: center;
                    padding: 80px;
                }
                .card {
                    background: #fff;
                    border: 3px solid #cc0000;
                    max-width: 400px;
                    margin: 0 auto;
                    padding: 30px;
                }
                h1 { color: #cc0000; font-size: 48px; margin: 0; }
                p { color: #666; }
                .hint { color: #999; font-size: 12px; margin-top: 30px; }
            </style>
        </head>
        <body>
            <div class="card">
                <h1>404</h1>
                <p>Почтовая служба временно не работает.</p>
                <p>Попробуйте другой способ отправки...</p>
            </div>
        </body>
        </html>
    `);
});

app.post('/flag', (req, res) => {
    res.json({ flag: 'flag{xss_1s_n0t_s0_sc4ry}' });
});

app.get('/', (req, res) => {
    res.send(`
        <html>
        <head>
            <title>Письмо Деду Морозу</title>
            <style>
                body {
                    background: #e8f4f8;
                    color: #333;
                    font-family: 'Tahoma', 'Arial', sans-serif;
                    margin: 0;
                    padding: 20px;
                }
                .card {
                    background: #fff;
                    border: 3px solid #cc0000;
                    max-width: 500px;
                    margin: 0 auto;
                    padding: 30px;
                    box-shadow: 5px 5px 15px rgba(0,0,0,0.1);
                }
                .card h1 {
                    color: #cc0000;
                    font-size: 22px;
                    text-align: center;
                    margin-top: 0;
                    border-bottom: 2px solid #cc0000;
                    padding-bottom: 15px;
                }
                .card label {
                    display: block;
                    font-size: 14px;
                    color: #555;
                    margin-top: 15px;
                }
                .card input[type="text"],
                .card textarea {
                    width: 100%;
                    padding: 8px;
                    border: 1px solid #ccc;
                    font-size: 14px;
                    box-sizing: border-box;
                    margin-top: 5px;
                }
                .card textarea {
                    height: 120px;
                    resize: none;
                }
                .card button {
                    background: #cc0000;
                    color: #fff;
                    border: none;
                    padding: 12px 30px;
                    font-size: 16px;
                    cursor: pointer;
                    margin-top: 20px;
                    width: 100%;
                }
                .card button:hover {
                    background: #aa0000;
                }
                .snowflake {
                    text-align: center;
                    font-size: 48px;
                    margin-bottom: 10px;
                }
                .footer {
                    text-align: center;
                    margin-top: 20px;
                    color: #999;
                    font-size: 11px;
                }
                .post-link {
                    text-align: center;
                    margin-top: 15px;
                }
                .post-link a {
                    color: #cc0000;
                    font-size: 12px;
                    text-decoration: none;
                    border: 1px solid #cc0000;
                    padding: 5px 10px;
                }
                .post-link a:hover {
                    background: #cc0000;
                    color: #fff;
                }
            </style>
        </head>
        <body>
            <div class="card">
                <div class="snowflake">&#10052;</div>
                <h1>&#9731; Письмо Деду Морозу &#9731;</h1>
                <form method="POST" action="/send">
                    <label>Твоё имя:</label>
                    <input type="text" name="name" placeholder="Как тебя зовут?" required>

                    <label>Твоё письмо:</label>
                    <textarea name="text" placeholder="Напиши, что ты хочешь на Новый год..." required></textarea>

                    <button type="submit">&#9993; Отправить письмо</button>
                </form>
                <div class="post-link">
                    <a href="/flag">&#9994; Почтовая служба Деда Мороза</a>
                </div>
            </div>
            <div class="footer">
                &copy; 2005 Письмо Деду Морозу | Работает на PHP 4.0
            </div>
        </body>
        </html>
    `);
});

app.post('/send', (req, res) => {
    const { name, text } = req.body;

    res.send(`
        <html>
        <head>
            <title>Письмо отправлено!</title>
            <style>
                body {
                    background: #e8f4f8;
                    color: #333;
                    font-family: 'Tahoma', 'Arial', sans-serif;
                    margin: 0;
                    padding: 20px;
                }
                .card {
                    background: #fff;
                    border: 3px solid #cc0000;
                    max-width: 500px;
                    margin: 0 auto;
                    padding: 30px;
                    box-shadow: 5px 5px 15px rgba(0,0,0,0.1);
                }
                .card h1 {
                    color: #cc0000;
                    font-size: 22px;
                    text-align: center;
                    margin-top: 0;
                    border-bottom: 2px solid #cc0000;
                    padding-bottom: 15px;
                }
                .letter {
                    background: #fff8dc;
                    border: 2px solid #daa520;
                    padding: 20px;
                    margin: 20px 0;
                    font-size: 14px;
                    line-height: 1.6;
                }
                .letter .name {
                    font-weight: bold;
                    color: #cc0000;
                }
                .back {
                    text-align: center;
                    margin-top: 20px;
                }
                .back a {
                    color: #cc0000;
                }
                .post-link {
                    text-align: center;
                    margin-top: 15px;
                }
                .post-link a {
                    color: #cc0000;
                    font-size: 12px;
                    text-decoration: none;
                    border: 1px solid #cc0000;
                    padding: 5px 10px;
                }
                .post-link a:hover {
                    background: #cc0000;
                    color: #fff;
                }
                .snowflake {
                    text-align: center;
                    font-size: 48px;
                    margin-bottom: 10px;
                }
                .footer {
                    text-align: center;
                    margin-top: 20px;
                    color: #999;
                    font-size: 11px;
                }
            </style>
        </head>
        <body>
            <div class="card">
                <div class="snowflake">&#10052;</div>
                <h1>&#9731; Письмо отправлено! &#9731;</h1>
                <p style="text-align:center;color:#666;">Дед Мороз получил твоё письмо и скоро прочитает!</p>
                <div class="letter">
                    <div class="name">${name || 'Дорогой друг'},</div>
                    <p>${text || '...'}</p>
                    <p style="text-align:right;color:#999;">С любовью, ${name || 'ты'}</p>
                </div>
                <div class="back">
                    <a href="/">&larr; Написать ещё письмо</a>
                </div>
                <div class="post-link">
                    <a href="/flag">&#9994; Почтовая служба Деда Мороза</a>
                </div>
            </div>
            <div class="footer">
                &copy; 2005 Письмо Деду Морозу | Работает на PHP 4.0
            </div>
        </body>
        </html>
    `);
});

app.listen(PORT, () => {
    console.log(`Task 5 (XSS) running on http://localhost:${PORT}`);
});
