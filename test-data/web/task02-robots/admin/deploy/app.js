const express = require('express');
const path = require('path');
const app = express();
const PORT = 3002;

app.use(express.static(path.join(__dirname)));

app.get('/', (req, res) => {
    res.send(`
        <html>
        <head>
            <title>Секретный архив</title>
            <style>
                * { margin: 0; padding: 0; box-sizing: border-box; }
                body {
                    background: #0a0a0a;
                    color: #cccccc;
                    font-family: 'Georgia', 'Times New Roman', serif;
                    padding: 40px;
                }
                .container {
                    border: 6px solid #8b0000;
                    background: linear-gradient(135deg, #0d0d0d 0%, #1a0000 100%);
                    margin: 0 auto;
                    width: 900px;
                    max-width: 100%;
                    box-shadow: 0 0 60px rgba(139, 0, 0, 0.4), inset 0 0 60px rgba(0,0,0,0.8);
                    position: relative;
                }
                .container::before {
                    content: '';
                    position: absolute;
                    top: 0; left: 0; right: 0; bottom: 0;
                    background: repeating-linear-gradient(
                        0deg,
                        transparent,
                        transparent 2px,
                        rgba(139, 0, 0, 0.03) 2px,
                        rgba(139, 0, 0, 0.03) 4px
                    );
                    pointer-events: none;
                }
                .header {
                    background: linear-gradient(180deg, #2a0000 0%, #1a0000 100%);
                    color: #ff4444;
                    font-size: 18px;
                    text-align: center;
                    padding: 25px;
                    border-bottom: 6px solid #8b0000;
                    font-family: 'Courier New', monospace;
                    letter-spacing: 4px;
                    text-shadow: 0 0 20px rgba(255, 0, 0, 0.3);
                }
                .header-sub {
                    font-size: 12px;
                    color: #884444;
                    margin-top: 8px;
                    letter-spacing: 6px;
                }
                .seal {
                    text-align: center;
                    padding: 10px 0 0 0;
                    font-size: 72px;
                    color: #8b0000;
                    opacity: 0.6;
                    text-shadow: 0 0 40px rgba(139, 0, 0, 0.3);
                }
                .stamp {
                    background: linear-gradient(135deg, #8b0000, #cc0000);
                    color: #ffffff;
                    font-size: 52px;
                    font-weight: bold;
                    text-align: center;
                    padding: 35px 20px;
                    border: 8px double #ff0000;
                    margin: 30px 60px;
                    font-family: 'Impact', 'Arial Black', sans-serif;
                    text-transform: uppercase;
                    letter-spacing: 8px;
                    transform: rotate(-3deg);
                    box-shadow: 0 0 40px rgba(255, 0, 0, 0.4), inset 0 0 30px rgba(0,0,0,0.3);
                    text-shadow: 3px 3px 0px #4a0000;
                    position: relative;
                }
                .stamp::after {
                    content: 'TOP SECRET';
                    position: absolute;
                    bottom: -12px;
                    right: -30px;
                    font-size: 14px;
                    color: #ff6666;
                    transform: rotate(15deg);
                    letter-spacing: 3px;
                    opacity: 0.7;
                }
                .content {
                    padding: 40px 50px;
                    text-align: justify;
                    line-height: 2;
                    font-size: 15px;
                    position: relative;
                }
                .content h2 {
                    color: #ff4444;
                    text-align: center;
                    font-size: 22px;
                    font-family: 'Courier New', monospace;
                    letter-spacing: 3px;
                    border-bottom: 2px solid #331111;
                    padding-bottom: 12px;
                    margin-bottom: 25px;
                    text-shadow: 0 0 10px rgba(255, 0, 0, 0.2);
                }
                .content p {
                    margin-bottom: 18px;
                    text-indent: 30px;
                }
                .content p:first-of-type {
                    text-indent: 0;
                }
                .footer {
                    background: linear-gradient(180deg, #1a0000, #0a0000);
                    color: #664444;
                    font-size: 11px;
                    text-align: center;
                    padding: 20px;
                    border-top: 6px solid #8b0000;
                    font-family: 'Courier New', monospace;
                    letter-spacing: 2px;
                }
                .blink {
                    animation: blink 1.5s step-end infinite;
                }
                @keyframes blink {
                    50% { opacity: 0; }
                }
                .signature {
                    text-align: right;
                    color: #886666;
                    font-style: italic;
                    margin-top: 30px;
                    padding-right: 20px;
                    border-top: 1px solid #331111;
                    padding-top: 20px;
                }
                .redacted {
                    background: #000000;
                    color: #000000;
                    padding: 0 8px;
                    cursor: help;
                    border-bottom: 1px dotted #331111;
                }
                .redacted:hover {
                    color: #ff4444;
                }
                .divider {
                    border: 0;
                    height: 1px;
                    background: linear-gradient(to right, transparent, #8b0000, transparent);
                    margin: 30px 0;
                    opacity: 0.5;
                }
                .warning-line {
                    color: #ff4444;
                    font-family: 'Courier New', monospace;
                    font-size: 11px;
                    text-align: center;
                    letter-spacing: 3px;
                    margin-bottom: 25px;
                    padding: 10px;
                    border: 1px solid #331111;
                    background: rgba(139, 0, 0, 0.1);
                }
                .watermark {
                    position: absolute;
                    top: 50%;
                    left: 50%;
                    transform: translate(-50%, -50%) rotate(-30deg);
                    font-size: 120px;
                    color: rgba(139, 0, 0, 0.03);
                    font-family: 'Impact', sans-serif;
                    letter-spacing: 20px;
                    pointer-events: none;
                    white-space: nowrap;
                }
                .classification {
                    display: flex;
                    justify-content: space-between;
                    padding: 5px 15px;
                    font-family: 'Courier New', monospace;
                    font-size: 10px;
                    color: #664444;
                    border-bottom: 1px solid #1a0000;
                }
            </style>
        </head>
        <body>
            <div class="container">

                <div class="content">
                    <div class="watermark">СЕКРЕТНО</div>

                    <p class="warning-line">[ ВНИМАНИЕ: ДАННЫЙ ДОКУМЕНТ ПРЕДНАЗНАЧЕН ТОЛЬКО ДЛЯ ЧЕЛОВЕЧЕСКОГО ГЛАЗА ]</p>

                    <hr class="divider">

                    <p>Многие века тайны этого архива передавались из уст в уста, от одного хранителя к другому. Говорят, что тот, кто осмелится проникнуть за пределы дозволенного, столкнётся с истиной, способной перевернуть всё мироздание. Но так ли это на самом деле? Или это лишь легенда, придуманная теми, кто боится, что когда-нибудь кто-то всё же откроет эту дверь?</p>

                    <p>За семью печатями, за семью замками, в самом сердце этого цифрового склепа хранятся записи, которые не должны быть увидены никем. Никем, кроме избранных. Никем, кроме тех, кто знает пароль. Никем, кроме тех, кто понимает, что некоторые вещи лучше оставить в покое.</p>

                    <p>Но, как говорили древние: <i>"Тайна существует лишь до тех пор, пока её не раскрыли"</i>. И если ты сейчас читаешь эти строки, значит, ты уже сделал первый шаг на пути к разгадке. Вопрос лишь в том, готов ли ты идти до конца?</p>

                    <hr class="divider">

                    <p>Архив был основан в 1998 году группой энтузиастов, которые называли себя <b>"Хранители Истины"</b>. Их целью было собрать в одном месте все знания, которые когда-либо были утеряны, спрятаны или уничтожены. Они верили, что информация должна быть доступна каждому, но при этом понимали, что некоторые знания могут быть опасны в руках неподготовленных.</p>

                    <p>Именно поэтому был создан этот архив. Именно поэтому доступ к нему так тщательно охраняется. Именно поэтому здесь, на этой странице, ты видишь лишь верхушку айсберга. Основные данные спрятаны глубоко, за множеством уровней защиты, и лишь самые стойкие искатели приключений смогут добраться до истины.</p>

                    <p>Поговаривают, что среди документов архива есть записи о <span class="redacted" title="[ДАННЫЕ УДАЛЕНЫ]">[ДАННЫЕ УДАЛЕНЫ]</span>, а также подробное описание <span class="redacted" title="[ДАННЫЕ УДАЛЕНЫ]">[ДАННЫЕ УДАЛЕНЫ]</span>. Но, конечно, это всего лишь слухи. Не так ли?</p>

                    <hr class="divider">

                    <h2>&#9762; Предупреждение &#9762;</h2>

                    <p>Если ты всё ещё читаешь этот текст, значит, ты либо очень любопытен, либо уже знаешь больше, чем следует. В любом случае, помни: некоторые двери открываются только перед теми, кто знает правильные слова. Или правильные пути.</p>

                    <p>Возможно, тебе стоит обратить внимание на те места, куда обычные посетители не заглядывают. Возможно, ответы скрыты там, где их меньше всего ожидают увидеть. Возможно, истина находится прямо перед тобой, но ты просто не знаешь, куда смотреть.</p>

                    <p>Но помни: если ты найдёшь то, что ищешь, обратного пути уже не будет. Ты станешь одним из Хранителей. И тогда уже тебе придётся решать, кому рассказать об этом, а кому — нет.</p>

                    <p class="signature">
                        С уважением,<br>
                        Хранитель Архива.<br>
                        <span style="font-size:11px;">15 мая 2005 года</span>
                    </p>

                </div>

                <div class="footer">
                    &#9762; СЕКРЕТНЫЙ АРХИВ ГЛАВНОГО УПРАВЛЕНИЯ &#9762;<br>
                    <span style="font-size:10px;">&copy; 2005 | Все права защищены | Нарушители будут наказаны по всей строгости закона</span><br>
                    <span style="font-size:9px;">Работает на Apache/1.3.33 (Unix) PHP/4.3.10 | MySQL 4.0.18</span>
                </div>
            </div>
        </body>
        </html>
    `);
});

app.get('/robots.txt', (req, res) => {
    res.type('text/plain');
    res.send(`
User-agent: *
Disallow: /sekretnyj-arhiv
    `.trim());
});

app.get('/sekretnyj-arhiv', (req, res) => {
    res.send(`
        <html>
        <head>
            <title>Ой</title>
            <style>
                body {
                    background: #1a1a2e;
                    color: #e0e0e0;
                    font-family: 'Comic Sans MS', 'Arial', sans-serif;
                    text-align: center;
                    padding: 60px 20px;
                }
                .card {
                    background: #16213e;
                    border: 3px solid #e94560;
                    border-radius: 20px;
                    padding: 40px;
                    max-width: 500px;
                    margin: 0 auto;
                    box-shadow: 0 0 30px rgba(233, 69, 96, 0.3);
                }
                h1 {
                    color: #e94560;
                    font-size: 48px;
                    margin: 0;
                }
                .flag {
                    font-size: 22px;
                    color: #0f3460;
                    background: #e94560;
                    padding: 15px 25px;
                    border-radius: 10px;
                    display: inline-block;
                    margin: 20px 0;
                    font-family: 'Courier New', monospace;
                    font-weight: bold;
                }
                .rickroll {
                    margin-top: 20px;
                    border: 3px solid #e94560;
                    border-radius: 10px;
                    max-width: 300px;
                }
                .sub {
                    color: #888;
                    font-size: 14px;
                }
                .lol {
                    font-size: 60px;
                    margin: 10px 0;
                }
            </style>
        </head>
        <body>
            <div class="card">
                <h1>heh</h1>
                <div class="flag">flag{r0b0ts_txt_1s_us3ful}</div>
                <img src="/dance-vibing.gif" alt="танец" class="rickroll">
            </div>
        </body>
        </html>
    `);
});

app.listen(PORT, () => {
    console.log(`Task 2 (robots.txt) running on http://localhost:${PORT}`);
});
