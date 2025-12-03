package main

import (
	"io"
	"os"
	"path/filepath"
)

// Storage управляет локальным хранилищем файлов
type Storage struct {
	BaseDir string // Базовая директория для хранения
}

// NewStorage создаёт новый экземпляр хранилища
func NewStorage(baseDir string) *Storage {
	return &Storage{
		BaseDir: baseDir,
	}
}

// SaveFile сохраняет файл в локальное хранилище
func (s *Storage) SaveFile(filename string, reader io.Reader) error {
	// Создаём полный путь к файлу
	// filepath.Join автоматически использует правильные разделители для ОС
	filePath := filepath.Join(s.BaseDir, filename)

	// Создаём файл на диске
	// os.Create создаёт новый файл или обрезает существующий
	dst, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer dst.Close()

	// Копируем данные из reader в файл
	// io.Copy эффективно копирует данные небольшими порциями
	_, err = io.Copy(dst, reader)
	return err
}

// GetFile возвращает файл для чтения
func (s *Storage) GetFile(filename string) (*os.File, error) {
	filePath := filepath.Join(s.BaseDir, filename)
	
	// Открываем файл только для чтения
	return os.Open(filePath)
}

// ListFiles возвращает список всех файлов в хранилище
func (s *Storage) ListFiles() ([]FileInfo, error) {
	var files []FileInfo

	// Читаем содержимое директории
	entries, err := os.ReadDir(s.BaseDir)
	if err != nil {
		return nil, err
	}

	// Проходим по всем файлам в директории
	for _, entry := range entries {
		// Пропускаем поддиректории
		if entry.IsDir() {
			continue
		}

		// Получаем информацию о файле
		info, err := entry.Info()
		if err != nil {
			continue
		}

		// Добавляем файл в список
		files = append(files, FileInfo{
			Name: entry.Name(),
			Size: info.Size(),
		})
	}

	return files, nil
}

// DeleteFile удаляет файл из хранилища
func (s *Storage) DeleteFile(filename string) error {
	filePath := filepath.Join(s.BaseDir, filename)
	return os.Remove(filePath)
}