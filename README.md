# CinemaAbyss — проект по архитектектуре

## Быстрый обзор
Сервисы:
- `src/monolith` — legacy монолит (Go + PostgreSQL)
- `src/microservices/movies` — movies-service (Go + PostgreSQL)
- `src/microservices/proxy` — proxy-service (API Gateway + Strangler Fig)
- `src/microservices/events` — events-service (REST → Kafka + consumer logs)

Тесты:
- `tests/postman` — Postman/Newman (Node.js)

---

## Локальный запуск (Docker Compose)

### Старт
```bash
docker compose up -d --build
