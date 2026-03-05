Required:
- Go
- Docker

Start:
- git clone
- go mod tidy
- go test ./internal/withdrawals -v

Using libs:
- slog
- gin
- pgx
- testify
- testcontainers-go
- google/uuid
- go-playground/validator
- shopspring/decimal



• Создание вывода средств выполняется внутри одной транзакции

• Перед созданием заявки выполняется чтение баланса пользователя: если баланс меньше требуемой суммы — операция завершается ошибкой

• Идемпотентность запросов:
  - для предотвращения повторного создания заявки используется idempotency_key
  - в таблице withdrawals установлен уникальный индекс: UNIQUE (user_id, idempotency_key)
  - выполняется "INSERT ... ON CONFLICT DO NOTHING"
  - если запись уже существует (withdrawal) — выполняется поиск по user_id и idempotency_key. Возвращается существующая заявка
  - выполняется проверка совпадения payload запроса, чтобы исключить конфликт разных операций с одинаковым ключом

• Конкурентная безопасность:
  - уровень изоляций транзакций - repeatable read, для избежания race condition и двойного списания средств при параллельности
  - есть блокировка строк - FOR UPDATE

• В БД происходит дополнительная проверка суммы - CHECK (amount > 0)

• Логирование покрыто тремя уровнями:
  - Info
  - Warn
  - Error