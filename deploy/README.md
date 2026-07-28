# Развёртывание

Приложение — один статический бинарник. В нём уже лежат фронтенд, миграции базы и получение
TLS-сертификатов. **nginx, certbot, Python, Node и sqlite3 на сервере не нужны.**

## 0. Что нужно заранее

Домен или поддомен, у которого A-запись указывает на IP сервера:

```bash
dig +short тренировки.example.ru
# должен вернуть IP сервера
```

Пока эта команда не вернёт нужный адрес, сертификат не выдадут.

**Проверьте, что порты свободны** — приложение занимает их само:

```bash
sudo ss -lntp | grep -E ':(80|443)\b'
# пусто — можно продолжать
```

Если 443 уже кем-то занят, схема без обратного прокси не подойдёт.

WireGuard работает по UDP и не конфликтует, но убедитесь, что firewall пропускает 80 и 443,
**не трогая порты туннеля**.

## 1. Собрать

На своей машине:

```bash
make release
# dist/gymtracker-linux-amd64
```

Кросс-компиляция работает без тулчейна: драйвер SQLite чистый на Go, cgo не используется.

## 2. Пользователь и каталоги на сервере

```bash
sudo useradd --system --home /opt/gymtracker --shell /usr/sbin/nologin gymtracker
sudo mkdir -p /opt/gymtracker/programs
```

## 3. Файлы

```bash
scp dist/gymtracker-linux-amd64 сервер:/tmp/gymtracker
scp programs/*.json               сервер:/tmp/
scp deploy/gymtracker.service     сервер:/tmp/

# на сервере
sudo install -m 755 -o gymtracker -g gymtracker /tmp/gymtracker /opt/gymtracker/gymtracker
sudo install -m 640 -o gymtracker -g gymtracker /tmp/*.json     /opt/gymtracker/programs/
sudo install -m 644 /tmp/gymtracker.service /etc/systemd/system/
```

## 4. Настройки

```bash
sudo tee /opt/gymtracker/.env > /dev/null << 'EOF'
GYM_DOMAIN=тренировки.example.ru
GYM_ACME_EMAIL=вы@example.ru
# Первое развёртывание — против тестового каталога Let's Encrypt.
GYM_ACME_STAGING=1
EOF

sudo chmod 600 /opt/gymtracker/.env
sudo chown gymtracker:gymtracker /opt/gymtracker/.env
```

Пароля в настройках нет: он задаётся при заведении пользователя и хранится только хешем.

## 5. Запуск

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now gymtracker
sudo systemctl status gymtracker        # active (running)
sudo journalctl -u gymtracker -n 50 --no-pager
```

Проверьте, что сертификат заказался (в тестовом каталоге браузер будет ругаться на
недоверенный сертификат — это ожидаемо):

```bash
curl -sk https://тренировки.example.ru/healthz
# {"ok":true}
```

**Только после этого** переходите на боевые сертификаты:

```bash
sudo sed -i '/GYM_ACME_STAGING/d' /opt/gymtracker/.env
sudo rm -rf /var/lib/gymtracker/autocert     # тестовые сертификаты больше не нужны
sudo systemctl restart gymtracker
```

Тестовый каталог здесь не формальность: Let's Encrypt разрешает **пять одинаковых
сертификатов за семь дней**. Ошибка в systemd, firewall или DNS при боевом заказе
блокирует домен на неделю.

## 6. Пользователь приложения

```bash
sudo -u gymtracker GYM_DB=/var/lib/gymtracker/gymtracker.db \
  /opt/gymtracker/gymtracker adduser igor --admin --name=Игорь
# Пароль: ...
```

Имя файла программы должно совпадать с именем пользователя: `programs/igor.json`.
Открытой регистрации через веб нет намеренно — приложение личное.

## 7. Firewall

```bash
sudo ufw allow 80,443/tcp
sudo ufw status                          # убедитесь, что порты WireGuard на месте
```

## 8. На телефон

Откройте `https://ваш-домен` в Safari → введите пароль → «Поделиться» → «На экран „Домой"».

**Внутри установленного приложения пароль спросят ещё раз** — у приложения с главного
экрана своё хранилище, отдельное от Safari. Дальше вход не понадобится месяцами:
срок токена продлевается при каждом обращении.

---

## Резервные копии

Копии делает сам процесс: при старте, затем раз в сутки и дополнительно после завершённой
тренировки, если предыдущая копия старше шести часов. Каждая копия проверяется на
целостность, битая — удаляется. Хранится 14 последних.

```
/var/lib/gymtracker/backups/db-20260728T040000Z.db
```

**Копия на том же диске — ещё не копия.** Забирайте её наружу:

```bash
curl -sf -b cookies.txt https://ваш-домен/api/admin/backup -o backup.db
```

Плюс кнопка «Выгрузить всё (JSON)» в приложении — полная выгрузка своих тренировок.
Её же принимает обратно подкоманда импорта:

```bash
sudo -u gymtracker GYM_DB=/var/lib/gymtracker/gymtracker.db \
  /opt/gymtracker/gymtracker import igor trenirovki-2026-07-28.json
```

Импорт сливает данные теми же правилами, что и синхронизация: его можно повторять,
и он не затирает более свежие записи.

## Смена программы тренировок

Программа задана файлом `programs/<имя-пользователя>.json` — у каждого своя.
Правки кода не требуется:

```bash
sudo -u gymtracker vi /opt/gymtracker/programs/igor.json
sudo systemctl restart gymtracker
```

Либо без перезапуска, из-под администратора:

```bash
curl -sf -b cookies.txt -X POST https://ваш-домен/api/admin/program/reload
```

Битый файл не принимается: при старте служба откажется подниматься с указанием
нарушенного правила, при перезагрузке вернёт 422 и оставит прежнюю программу.

**Правило, которое нельзя нарушать: `exercise_id` вечен.** Меняете упражнение — заводите
новый id. Освободившийся id никогда не переиспользуйте под другое упражнение, иначе
в один график склеятся присед и жим. Данные по упражнениям, выпавшим из программы,
остаются в базе и в выгрузке, а история продолжает рисоваться снапшотом той программы,
по которой была записана.

## Обновление

```bash
make release
scp dist/gymtracker-linux-amd64 сервер:/tmp/gymtracker
sudo install -m 755 -o gymtracker -g gymtracker /tmp/gymtracker /opt/gymtracker/gymtracker
sudo systemctl restart gymtracker
```

Миграции базы применяются при старте автоматически.

## Второй пользователь

Модель данных к этому готова с первого дня, миграции не потребуется:

```bash
sudo -u gymtracker GYM_DB=/var/lib/gymtracker/gymtracker.db \
  /opt/gymtracker/gymtracker adduser lena --name=Лена
sudo -u gymtracker vi /opt/gymtracker/programs/lena.json   # своя программа
sudo systemctl restart gymtracker
```

Истории, программы и выгрузки полностью разделены. Останется добавить поле имени
на экране входа — сейчас, пока пользователь один, там только поле пароля.
