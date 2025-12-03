package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

// Node представляет собой узел распределённой системы
type Node struct {
	Port       string        // Порт, на котором слушает узел
	Peers      []string      // Список адресов других узлов
	StorageDir string        // Директория для хранения файлов
	Storage    *Storage      // Менеджер локального хранилища
	mu         sync.RWMutex  // Мьютекс для потокобезопасности
}

// FileInfo содержит метаданные о файле
type FileInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// NewNode создаёт новый экземпляр узла
func NewNode(port string, peers []string, storageDir string) *Node {
	return &Node{
		Port:       port,
		Peers:      peers,
		StorageDir: storageDir,
		Storage:    NewStorage(storageDir),
	}
}

// Start запускает HTTP сервер узла
func (n *Node) Start() error {
	// Регистрируем обработчики HTTP-запросов
	// Каждый обработчик отвечает за свой тип операций
	
	http.HandleFunc("/upload", n.handleUpload)       // Загрузка файла
	http.HandleFunc("/download/", n.handleDownload)  // Скачивание файла
	http.HandleFunc("/list", n.handleList)           // Список файлов
	http.HandleFunc("/sync", n.handleSync)           // Синхронизация с другими узлами
	http.HandleFunc("/health", n.handleHealth)       // Проверка работоспособности
	
	// Запускаем фоновую синхронизацию с другими узлами
	// Это горутина (аналог потока), которая работает параллельно
	go n.periodicSync()

	// Запускаем HTTP сервер
	addr := ":" + n.Port
	log.Printf("Сервер слушает на %s", addr)
	return http.ListenAndServe(addr, nil)
}

// handleUpload обрабатывает загрузку файла от клиента
func (n *Node) handleUpload(w http.ResponseWriter, r *http.Request) {
	// Проверяем, что используется метод POST
	if r.Method != http.MethodPost {
		http.Error(w, "Только POST метод разрешён", http.StatusMethodNotAllowed)
		return
	}

	// Парсим multipart/form-data (формат для загрузки файлов)
	// 10 << 20 означает максимальный размер 10 МБ в памяти
	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		http.Error(w, "Ошибка парсинга формы", http.StatusBadRequest)
		return
	}

	// Получаем файл из запроса
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Ошибка получения файла", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Сохраняем файл локально
	err = n.Storage.SaveFile(header.Filename, file)
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка сохранения файла: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Файл сохранён: %s", header.Filename)

	// Реплицируем файл на другие узлы для отказоустойчивости
	// Это происходит асинхронно, чтобы не замедлять ответ клиенту
	go n.replicateFile(header.Filename)

	// Отправляем успешный ответ клиенту
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Файл успешно загружен",
		"file":    header.Filename,
	})
}

// handleDownload обрабатывает скачивание файла клиентом
func (n *Node) handleDownload(w http.ResponseWriter, r *http.Request) {
	// Извлекаем имя файла из URL
	// Например, для /download/test.txt получим "test.txt"
	filename := filepath.Base(r.URL.Path)

	// Пытаемся открыть файл локально
	file, err := n.Storage.GetFile(filename)
	if err != nil {
		// Если файла нет локально, пытаемся найти его на других узлах
		log.Printf("Файл %s не найден локально, запрашиваем у соседей", filename)
		
		content, err := n.fetchFileFromPeers(filename)
		if err != nil {
			http.Error(w, "Файл не найден", http.StatusNotFound)
			return
		}
		
		// Сохраняем полученный файл локально для будущих запросов
		n.Storage.SaveFile(filename, bytes.NewReader(content))
		
		// Отправляем файл клиенту
		w.Header().Set("Content-Disposition", "attachment; filename="+filename)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(content)
		return
	}
	defer file.Close()

	// Отправляем файл клиенту
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	w.Header().Set("Content-Type", "application/octet-stream")
	io.Copy(w, file)
	
	log.Printf("📤 Файл отправлен: %s", filename)
}

// handleList возвращает список всех файлов в системе
func (n *Node) handleList(w http.ResponseWriter, r *http.Request) {
	files, err := n.Storage.ListFiles()
	if err != nil {
		http.Error(w, "Ошибка получения списка файлов", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

// handleSync обрабатывает запрос на синхронизацию от другого узла
func (n *Node) handleSync(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		// Возвращаем список наших файлов
		files, err := n.Storage.ListFiles()
		if err != nil {
			http.Error(w, "Ошибка получения списка", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(files)
	} else if r.Method == http.MethodPost {
		// Получаем файл от другого узла
		n.handleUpload(w, r)
	}
}

// handleHealth проверяет, работает ли узел
func (n *Node) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

// replicateFile отправляет файл на все остальные узлы
func (n *Node) replicateFile(filename string) {
	file, err := n.Storage.GetFile(filename)
	if err != nil {
		log.Printf("❌ Ошибка чтения файла для репликации: %v", err)
		return
	}
	defer file.Close()

	// Читаем содержимое файла в память
	content, err := io.ReadAll(file)
	if err != nil {
		log.Printf("❌ Ошибка чтения содержимого файла: %v", err)
		return
	}

	// Отправляем файл на каждый узел из списка соседей
	for _, peer := range n.Peers {
		go func(peerAddr string) {
			err := n.sendFileToPeer(peerAddr, filename, content)
			if err != nil {
				log.Printf("⚠️  Не удалось реплицировать на %s: %v", peerAddr, err)
			} else {
				log.Printf("✅ Файл реплицирован на %s", peerAddr)
			}
		}(peer)
	}
}

// sendFileToPeer отправляет файл конкретному узлу
func (n *Node) sendFileToPeer(peerAddr, filename string, content []byte) error {
	// Создаём multipart форму для отправки файла
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	
	part.Write(content)
	writer.Close()

	// Отправляем POST запрос
	url := fmt.Sprintf("http://%s/sync", peerAddr)
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return err
	}
	
	req.Header.Set("Content-Type", writer.FormDataContentType())
	
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("статус ответа: %d", resp.StatusCode)
	}

	return nil
}

// fetchFileFromPeers пытается получить файл от других узлов
func (n *Node) fetchFileFromPeers(filename string) ([]byte, error) {
	for _, peer := range n.Peers {
		url := fmt.Sprintf("http://%s/download/%s", peer, filename)
		
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return io.ReadAll(resp.Body)
		}
	}
	
	return nil, fmt.Errorf("файл не найден ни на одном узле")
}

// periodicSync периодически синхронизируется с другими узлами
func (n *Node) periodicSync() {
	// Ждём 5 секунд перед первой синхронизацией
	time.Sleep(5 * time.Second)
	
	// Создаём тикер, который срабатывает каждые 30 секунд
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		n.syncWithPeers()
	}
}

// syncWithPeers синхронизирует файлы со всеми соседними узлами
func (n *Node) syncWithPeers() {
	for _, peer := range n.Peers {
		go func(peerAddr string) {
			// Получаем список файлов с соседнего узла
			url := fmt.Sprintf("http://%s/sync", peerAddr)
			client := &http.Client{Timeout: 5 * time.Second}
			
			resp, err := client.Get(url)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			var peerFiles []FileInfo
			if err := json.NewDecoder(resp.Body).Decode(&peerFiles); err != nil {
				return
			}

			// Проверяем, каких файлов у нас нет
			localFiles, _ := n.Storage.ListFiles()
			localFileMap := make(map[string]bool)
			for _, f := range localFiles {
				localFileMap[f.Name] = true
			}

			// Запрашиваем недостающие файлы
			for _, peerFile := range peerFiles {
				if !localFileMap[peerFile.Name] {
					content, err := n.fetchFileFromPeers(peerFile.Name)
					if err == nil {
						n.Storage.SaveFile(peerFile.Name, bytes.NewReader(content))
						log.Printf("🔄 Синхронизирован файл: %s", peerFile.Name)
					}
				}
			}
		}(peer)
	}
}