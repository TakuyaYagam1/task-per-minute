const express = require('express');
const path = require('path');
const app = express();
const PORT = 3003;

app.use(express.static(path.join(__dirname)));

app.get('/', (req, res) => {
    res.send(`
        <html>
        <head>
            <title>Кофейня "У Самовара"</title>
            <style>
                body {
                    background: #ffffff;
                    color: #333333;
                    font-family: 'Tahoma', 'Arial', sans-serif;
                    margin: 0;
                    padding: 0;
                }
                .header {
                    background: #8B4513;
                    color: #ffffff;
                    text-align: center;
                    padding: 30px 20px;
                    font-size: 28px;
                    font-weight: bold;
                }
                .header span {
                    font-size: 14px;
                    font-weight: normal;
                    display: block;
                    margin-top: 5px;
                    color: #d4a574;
                }
                .gallery {
                    max-width: 700px;
                    margin: 20px auto;
                    padding: 0 20px;
                    text-align: center;
                }
                .gallery img {
                    max-width: 100%;
                    height: auto;
                    border: 1px solid #ddd;
                    margin: 10px 0;
                }
                .gallery .row {
                    display: flex;
                    gap: 10px;
                    flex-wrap: wrap;
                    justify-content: center;
                }
                .gallery .row img {
                    max-width: 48%;
                    flex: 1 1 200px;
                }
                .menu {
                    max-width: 600px;
                    margin: 30px auto;
                    padding: 0 20px;
                }
                .menu h2 {
                    color: #8B4513;
                    border-bottom: 2px solid #8B4513;
                    padding-bottom: 10px;
                    text-align: center;
                }
                .item {
                    padding: 15px;
                    border-bottom: 1px solid #eee;
                }
                .item:last-child {
                    border-bottom: none;
                }
                .item .name {
                    font-weight: bold;
                    font-size: 18px;
                }
                .item .price {
                    float: right;
                    color: #8B4513;
                    font-weight: bold;
                }
                .item .desc {
                    color: #666;
                    font-size: 13px;
                    margin-top: 5px;
                }
                .reviews {
                    max-width: 600px;
                    margin: 30px auto;
                    padding: 0 20px;
                }
                .reviews h2 {
                    color: #8B4513;
                    border-bottom: 2px solid #8B4513;
                    padding-bottom: 10px;
                    text-align: center;
                }
                .review {
                    background: #f9f9f9;
                    border: 1px solid #eee;
                    padding: 15px;
                    margin: 15px 0;
                    border-radius: 5px;
                }
                .review .author {
                    font-weight: bold;
                    color: #8B4513;
                    margin-top: 10px;
                }
                .review .stars {
                    color: #ffa500;
                    font-size: 18px;
                }
                .review .text {
                    font-style: italic;
                    color: #555;
                }
                .footer {
                    background: #333;
                    color: #999;
                    text-align: center;
                    padding: 20px;
                    font-size: 12px;
                    margin-top: 40px;
                }
                .notice {
                    background: #fff3cd;
                    border: 1px solid #ffc107;
                    color: #856404;
                    padding: 15px;
                    text-align: center;
                    margin: 20px auto;
                    max-width: 600px;
                    font-size: 14px;
                }
                .contact {
                    text-align: center;
                    margin: 20px auto;
                    max-width: 600px;
                    padding: 20px;
                    background: #f9f9f9;
                    border: 1px solid #ddd;
                }
                .contact a {
                    color: #8B4513;
                }
                .about {
                    max-width: 600px;
                    margin: 30px auto;
                    padding: 0 20px;
                    text-align: center;
                }
                .about h2 {
                    color: #8B4513;
                    border-bottom: 2px solid #8B4513;
                    padding-bottom: 10px;
                }
            </style>
        </head>
        <body>
            <div class="header">
                &#9749; Кофейня "У Самовара"
                <span>Уютное местечко</span>
            </div>

            <div class="gallery">
                <img src="/image.png" alt="Наше кафе">
                <div class="row">
                    <img src="/orig.jpg" alt="Интерьер">
                    <img src="/e6c220bb8fad89334c796d0f7126b18e.jpg" alt="Атмосфера">
                </div>
            </div>

            <div class="about">
                <h2>О нас</h2>
                <p>Кофейня "У Самовара" — это уютное место в самом центре д. Большой Пинеж, где вы можете отдохнуть и насладиться атмосферой тепла и уюта. Мы работаем с 1992 года и радуем наших гостей вкусным чаем и домашней выпечкой.</p>
            </div>

            <div class="notice">
                <b>&#9888; Уважаемые посетители!</b><br>
                По техническим причинам кофе временно не подаётся.<br>
                К вашим услугам только чай. Приносим извинения за неудобства.<br>
                <span style="font-size:12px;">Подробности: <a href="/flag" style="color:#856404;">почему нет кофе?</a></span>
            </div>

            <div class="menu">
                <h2>Наше меню</h2>

                <div class="item">
                    <span class="name">Чай чёрный</span>
                    <span class="price">50 &#8381;</span>
                    <div class="desc">Классический чёрный чай. Сахар по желанию.</div>
                </div>
                <div class="item">
                    <span class="name">Чай зелёный</span>
                    <span class="price">60 &#8381;</span>
                    <div class="desc">Лёгкий и освежающий зелёный чай.</div>
                </div>
                <div class="item">
                    <span class="name">Чай с бергамотом</span>
                    <span class="price">70 &#8381;</span>
                    <div class="desc">Английская классика. Граф Грей, собственной персоной.</div>
                </div>
                <div class="item">
                    <span class="name">Чай облепиховый</span>
                    <span class="price">90 &#8381;</span>
                    <div class="desc">Облепиха, мёд, имбирь. Для тех, кто мёрзнет.</div>
                </div>
                <div class="item">
                    <span class="name">Чай малиновый</span>
                    <span class="price">80 &#8381;</span>
                    <div class="desc">С вареньем. Как у бабушки.</div>
                </div>
                <div class="item">
                    <span class="name">Чай с лимоном</span>
                    <span class="price">60 &#8381;</span>
                    <div class="desc">Лимон, сахар, кружка. Минимализм.</div>
                </div>
            </div>

            <div class="reviews">
                <h2>Отзывы наших гостей</h2>

                <div class="review">
                    <div class="stars">&#9733;&#9733;&#9733;&#9733;&#9733;</div>
                    <div class="text">"Лучшее место в деревне! Обожаю их чай с бергамотом. Атмосфера невероятная, персонал приветливый. Всем советую!"</div>
                    <div class="author">— Анна С., постоянный гость</div>
                </div>

                <div class="review">
                    <div class="stars">&#9733;&#9733;&#9733;&#9733;&#9733;</div>
                    <div class="text">"Очень уютно. Чай облепиховый — просто божественный. Жаль, что кофе не подают, но чай тут на высоте!"</div>
                    <div class="author">— Дмитрий П.</div>
                </div>

                <div class="review">
                    <div class="stars">&#9733;&#9733;&#9733;&#9733;&#9734;</div>
                    <div class="text">"Хорошее место, чтобы посидеть с друзьями. Цены приятные, порции большие. Рекомендую малиновый чай!"</div>
                    <div class="author">— Елена К.</div>
                </div>

                <div class="review">
                    <div class="stars">&#9733;&#9733;&#9733;&#9733;&#9733;</div>
                    <div class="text">"Были с семьёй, очень понравилось. Детям дали дополнительное варенье бесплатно. Обязательно вернёмся!"</div>
                    <div class="author">— Сергей М.</div>
                </div>
            </div>

            <div class="contact">
                <b>Контакты:</b><br>
                д. Большой Пинеж, ул. Чайная, д. 1<br>
                Тел.: 8-800-555-35-35<br>
                E-mail: <a href="mailto:tea@samovar.ru">tea@samovar.ru</a><br><br>
                <span style="color:#999;font-size:12px;">Работаем ежедневно с 8:00 до 22:00</span>
            </div>

            <div class="footer">
                &copy; 1992 Кофейня "У Самовара" | ИП Самоваров А.И.<br>
                <span style="font-size:10px;">Сайт создан в конструкторе сайтов "Народ"</span>
            </div>
        </body>
        </html>
    `);
});

app.get('/flag', (req, res) => {
    res.status(418)
       .set('X-Flag', 'flag{1m_a_t34p0t_4nd_pr0ud}')
       .send(`
        <html>
        <head>
            <title>418</title>
            <style>
                body {
                    background: #fff;
                    color: #333;
                    font-family: 'Tahoma', sans-serif;
                    text-align: center;
                    padding: 100px 20px;
                }
                h1 {
                    font-size: 72px;
                    color: #ccc;
                    margin: 0;
                }
                p {
                    color: #999;
                    font-size: 16px;
                }
            </style>
        </head>
        <body>
            <h1>418</h1>
            <p>I'm a Teapot</p>
            <p>Страница недоступна. Попробуйте позже.</p>
        </body>
        </html>
    `);
});

app.listen(PORT, () => {
    console.log(`Task 3 (Status 418) running on http://localhost:${PORT}`);
});
