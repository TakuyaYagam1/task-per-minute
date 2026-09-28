const express = require('express');
const path = require('path');
const app = express();
const PORT = 3001;

app.use(express.static(path.join(__dirname)));

app.get('/', (req, res) => {
    res.cookie('flag', 'c00ki3_m0nst3r_1337', {
        httpOnly: false,
        maxAge: 3600000
    });
    res.send(`
        <html>
        <head>
            <title>Мой супер сайт</title>
            <style>
                body {
                    background: #000080;
                    color: #c0c0c0;
                    font-family: 'Comic Sans MS', 'Arial', sans-serif;
                    margin: 0;
                    padding: 0;
                }
                table {
                    border: 2px solid #c0c0c0;
                    background: #000000;
                    margin: 50px auto;
                    width: 600px;
                }
                td {
                    padding: 10px;
                }
                .header {
                    background: #000080;
                    color: #ffffff;
                    font-size: 24px;
                    font-weight: bold;
                    text-align: center;
                    padding: 15px;
                    border-bottom: 2px solid #c0c0c0;
                }
                .content {
                    background: #000000;
                    padding: 20px;
                    text-align: center;
                }
                .footer {
                    background: #000080;
                    color: #808080;
                    font-size: 11px;
                    text-align: center;
                    padding: 10px;
                    border-top: 2px solid #c0c0c0;
                }
                .counter {
                    color: #00ff00;
                    font-family: 'Courier New', monospace;
                    font-size: 14px;
                }
                hr {
                    border: 1px solid #808080;
                }
                .blink {
                    animation: blink 1s step-end infinite;
                }
                @keyframes blink {
                    50% { opacity: 0; }
                }
                .meme {
                    max-width: 100%;
                    height: auto;
                    border: 2px solid #ff0000;
                }
            </style>
        </head>
        <body>
            <table>
                <tr>
                    <td class="header">
                        <span class="blink">&#9733;</span> Добро пожаловать на мой сайт! <span class="blink">&#9733;</span>
                    </td>
                </tr>
                <tr>
                    <td class="content">
                        <p><img src="data:image/gif;base64,R0lGODlhEAAQAPABAAAAAP///yH/C05FVFNDQVBFMi4wAwEAAAAh+QQFAAABACwAAAAAEAAQAAACDISPqcvtD6OcNqBLAAA7" alt="Under construction" style="width:80px;height:15px;"></p>
                        <h1>Привет, гость!</h1>
                        <p>Рад видеть тебя на моём сайте!</p>
                        <p>Тут скоро будет много интересного контента...</p>
                        <p>А пока посмотри какой прикол:</p>
                        <img src="/image.png" alt="meme" class="meme">
                        <hr>
                        <p class="counter">&#8470; посетителей: 1337</p>
                    </td>
                </tr>
                <tr>
                    <td class="footer">
                        &copy; 2005 Мой Супер Сайт | Лучший сайт в рунете!<br>
                        <span style="font-size:10px;">ICQ: 123-456-789</span>
                    </td>
                </tr>
            </table>
        </body>
        </html>
    `);
});

app.listen(PORT, () => {
    console.log(`Task 1 (Cookie) running on http://localhost:${PORT}`);
});
