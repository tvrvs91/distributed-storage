# Используем официальный образ Go для сборки приложения
FROM golang:1.25.5-alpine AS builder

# Устанавливаем рабочую директорию внутри контейнера
WORKDIR /app

# Копируем файлы go.mod и go.sum для загрузки зависимостей
# Это делается отдельным шагом для кэширования слоя Docker
COPY go.mod ./

# Загружаем зависимости
RUN go mod download

# Копируем весь исходный код
COPY *.go ./

# Компилируем приложение
# -o указывает имя выходного файла
RUN go build -o distributed-storage .

# Используем минимальный образ для запуска приложения
# Это уменьшает размер финального образа
FROM alpine:latest

# Устанавливаем необходимые пакеты
RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Копируем скомпилированное приложение из builder stage
COPY --from=builder /app/distributed-storage .

# Создаём директорию для хранения файлов
RUN mkdir -p /root/storage

# Открываем порт для HTTP сервера
EXPOSE 8080

# Команда запуска приложения
ENTRYPOINT ["./distributed-storage"]