package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

func main() {
	fmt.Println("Ozon reporting data grouping device 700 v0.4.2")
	dir, err := os.Getwd()
	if err != nil {
		fmt.Println("Ошибка при получении текущей директории:", err)
		return
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.xlsx"))
	if err != nil {
		fmt.Println("Ошибка при поиске .xlsx файлов:", err)
		return
	}

	if len(files) == 0 {
		fmt.Println("Не найдено .xlsx файлов в текущей директории")
		waitForAnyKey()
		return
	}

	groupingSettings, err := readGroupingSettings("settings.txt")
	if err != nil {
		fmt.Println("Ошибка при чтении файла настроек:", err)
		waitForAnyKey()
		return
	}

	for _, file := range files {
		fmt.Printf("Обработка файла: %s\n", file)
		processExcelFile(file, groupingSettings)
	}

	fmt.Println("\nОбработка всех файлов завершена.")
	waitForAnyKey()
}

type GroupingSetting struct {
	SourceColumn string
	TargetColumn string
	MarkAsD      bool
}

// Очистка заголовков
func cleanHeader(header string) string {
	// Убираем переносы строк и лишние пробелы
	header = strings.ReplaceAll(header, "\n", " ")
	header = strings.ReplaceAll(header, "\r", "")
	header = strings.TrimSpace(header)
	// Заменяем множественные пробелы на один
	return strings.Join(strings.Fields(header), " ")
}

func readGroupingSettings(filename string) ([]GroupingSetting, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var settings []GroupingSetting

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		// Удаляем BOM (Byte Order Mark) если присутствует
		if strings.HasPrefix(line, "\uFEFF") {
			line = strings.TrimPrefix(line, "\uFEFF")
		}

		parts := strings.Split(line, "#")
		if len(parts) < 2 {
			continue
		}

		source := strings.TrimSpace(parts[0])
		target := strings.TrimSpace(parts[1])
		fmt.Print(source)
		fmt.Print("|")
		fmt.Println(target)
		markAsD := false
		if len(parts) >= 3 && strings.TrimSpace(parts[2]) == "Д" {
			markAsD = true
		}

		if source != "" && target != "" {
			settings = append(settings, GroupingSetting{
				SourceColumn: source,
				TargetColumn: target,
				MarkAsD:      markAsD,
			})
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return settings, nil
}

func processExcelFile(filePath string, groupingSettings []GroupingSetting) {
	f, err := excelize.OpenFile(filePath)
	if err != nil {
		fmt.Printf("Ошибка при открытии файла %s: %v\n", filePath, err)
		return
	}
	defer func() {
		if err := f.Close(); err != nil {
			fmt.Printf("Ошибка при закрытии файла %s: %v\n", filePath, err)
		}
	}()

	processReturns(f)
	processAcquiring(f)
	groupData(f, groupingSettings)

	if err := f.Save(); err != nil {
		fmt.Printf("Ошибка при сохранении файла %s: %v\n", filePath, err)
		return
	}

	fmt.Printf("Файл %s успешно обработан и сохранен\n", filePath)
}

// отделяем возвраты
func processReturns(f *excelize.File) {
	rows, err := f.GetRows("Начисления")
	if err != nil {
		fmt.Printf("Ошибка при чтении листа 'Начисления': %v\n", err)
		return
	}

	if len(rows) < 3 {
		fmt.Println("Лист 'Начисления' содержит недостаточно данных")
		return
	}

	// Создаем или очищаем лист для возвратов
	index, err := f.GetSheetIndex("grouping возвраты")
	if err == nil && index > 0 {
		f.DeleteSheet("grouping возвраты")
	}
	_, err = f.NewSheet("grouping возвраты")
	if err != nil {
		fmt.Printf("Ошибка при создании листа 'grouping возвраты': %v\n", err)
		return
	}

	headers := rows[1]
	for i, header := range headers {
		headers[i] = cleanHeader(header)
	}
	colIndexes := make(map[string]int)
	for i, header := range headers {
		colIndexes[header] = i + 1
	}

	requiredCols := []string{"ID начисления", "Группа услуг", "Тип начисления", "Сумма итого, руб"}
	for _, col := range requiredCols {
		if _, exists := colIndexes[col]; !exists {
			fmt.Printf("Не найден обязательный столбец: %s\n", col)
			return
		}
	}

	type ReturnData struct {
		ID                 string
		Sum                float64
		VoznagrazhdenieSum float64 // Новая колонка для возврата вознаграждения
		FullData           []string
		HasVoznagrazhdenie bool // Флаг, что есть возврат вознаграждения
	}

	returnsMap := make(map[string]ReturnData)

	for _, row := range rows[2:] {
		if len(row) < len(headers) {
			continue
		}

		group := row[colIndexes["Группа услуг"]-1]
		tipNach := row[colIndexes["Тип начисления"]-1]

		// Проверяем оба условия: Группа услуг = Возвраты ИЛИ Тип начисления = Возврат вознаграждения
		if group != "Возвраты" && tipNach != "Возврат вознаграждения" {
			continue
		}

		id := row[colIndexes["ID начисления"]-1]

		sumStr := row[colIndexes["Сумма итого, руб"]-1]
		cleanedSum := strings.Map(func(r rune) rune {
			if r == '-' || (r >= '0' && r <= '9') {
				return r
			}
			return -1
		}, sumStr)

		sum, err := strconv.ParseFloat(cleanedSum, 64)
		if err != nil {
			fmt.Printf("Ошибка парсинга суммы '%s': %v\n", sumStr, err)
			continue
		}
		sum = sum / 100

		if existing, exists := returnsMap[id]; exists {
			if tipNach == "Возврат вознаграждения" {
				// Суммируем в отдельную колонку для возврата вознаграждения
				existing.VoznagrazhdenieSum += sum
				existing.HasVoznagrazhdenie = true
			} else {
				// Суммируем в основную сумму для возвратов
				existing.Sum += sum
			}
			returnsMap[id] = existing
		} else {
			// Новая запись
			returnData := ReturnData{
				ID:       id,
				Sum:      sum,
				FullData: row,
			}
			if tipNach == "Возврат вознаграждения" {
				returnData.VoznagrazhdenieSum = sum
				returnData.HasVoznagrazhdenie = true
				returnData.Sum = 0 // Основная сумма для возврата вознаграждения = 0
			}
			returnsMap[id] = returnData
		}
	}

	// Формируем заголовки (без "Тип начисления", но с новой колонкой)
	var outputHeaders []string
	for _, header := range headers {
		if header != "Тип начисления" {
			outputHeaders = append(outputHeaders, header)
		}
	}
	// Добавляем новую колонку для возврата вознаграждения
	outputHeaders = append(outputHeaders, "Возврат вознаграждения, руб")

	// Записываем заголовки
	for i, header := range outputHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue("grouping возвраты", cell, header)
	}

	// Записываем данные
	rowNum := 2
	for _, ret := range returnsMap {
		colNum := 1
		for _, header := range headers {
			if header == "Тип начисления" {
				continue // Пропускаем этот столбец
			}

			cell, _ := excelize.CoordinatesToCellName(colNum, rowNum)
			if header == "ID начисления" {
				f.SetCellValue("grouping возвраты", cell, ret.ID)
			} else if header == "Сумма итого, руб" {
				f.SetCellValue("grouping возвраты", cell, ret.Sum)
			} else {
				// Берем значение из оригинальной строки
				origIndex := colIndexes[header] - 1
				if origIndex < len(ret.FullData) {
					f.SetCellValue("grouping возвраты", cell, ret.FullData[origIndex])
				}
			}
			colNum++
		}

		// Записываем возврат вознаграждения в последнюю колонку
		cell, _ := excelize.CoordinatesToCellName(colNum, rowNum)
		f.SetCellValue("grouping возвраты", cell, ret.VoznagrazhdenieSum)

		rowNum++
	}

	fmt.Printf("Найдено и перенесено %d возвратов (включая возвраты вознаграждения)\n", len(returnsMap))
}

// отделяем эквайринг
func processAcquiring(f *excelize.File) {
	rows, err := f.GetRows("Начисления")
	if err != nil {
		fmt.Printf("Ошибка при чтении листа 'Начисления': %v\n", err)
		return
	}

	if len(rows) < 3 {
		fmt.Println("Лист 'Начисления' содержит недостаточно данных")
		return
	}

	// Создаем или очищаем лист для эквайринга
	index, err := f.GetSheetIndex("grouping эквайринг")
	if err == nil && index > 0 {
		f.DeleteSheet("grouping эквайринг")
	}
	_, err = f.NewSheet("grouping эквайринг")
	if err != nil {
		fmt.Printf("Ошибка при создании листа 'grouping эквайринг': %v\n", err)
		return
	}

	headers := rows[1]
	for i, header := range headers {
		headers[i] = cleanHeader(header)
	}
	colIndexes := make(map[string]int)
	for i, header := range headers {
		colIndexes[header] = i + 1
	}

	requiredCols := []string{"ID начисления", "Тип начисления", "Сумма итого, руб"}
	for _, col := range requiredCols {
		if _, exists := colIndexes[col]; !exists {
			fmt.Printf("Не найден обязательный столбец: %s\n", col)
			return
		}
	}

	type AcquiringData struct {
		ID       string
		Sum      float64
		FullData []string
	}

	acquiringMap := make(map[string]AcquiringData)

	for _, row := range rows[2:] {
		if len(row) < len(headers) {
			continue
		}

		tipNach := row[colIndexes["Тип начисления"]-1]
		if tipNach != "Эквайринг" {
			continue
		}

		id := row[colIndexes["ID начисления"]-1]

		sumStr := row[colIndexes["Сумма итого, руб"]-1]
		cleanedSum := strings.Map(func(r rune) rune {
			if r == '-' || (r >= '0' && r <= '9') {
				return r
			}
			return -1
		}, sumStr)

		sum, err := strconv.ParseFloat(cleanedSum, 64)
		if err != nil {
			fmt.Printf("Ошибка парсинга суммы '%s': %v\n", sumStr, err)
			continue
		}
		sum = sum / 100

		if existing, exists := acquiringMap[id]; exists {
			// Суммируем суммы для одинаковых ID
			existing.Sum += sum
			acquiringMap[id] = existing
		} else {
			// Новая запись
			acquiringMap[id] = AcquiringData{
				ID:       id,
				Sum:      sum,
				FullData: row,
			}
		}
	}

	// Записываем заголовки
	for i, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue("grouping эквайринг", cell, header)
	}

	// Записываем данные
	rowNum := 2
	for _, acq := range acquiringMap {
		colNum := 1
		for _, header := range headers {
			cell, _ := excelize.CoordinatesToCellName(colNum, rowNum)
			if header == "ID начисления" {
				f.SetCellValue("grouping эквайринг", cell, acq.ID)
			} else if header == "Сумма итого, руб" {
				f.SetCellValue("grouping эквайринг", cell, acq.Sum)
			} else {
				// Берем значение из оригинальной строки
				origIndex := colIndexes[header] - 1
				if origIndex < len(acq.FullData) {
					f.SetCellValue("grouping эквайринг", cell, acq.FullData[origIndex])
				}
			}
			colNum++
		}
		rowNum++
	}

	fmt.Printf("Найдено и перенесено %d записей эквайринга\n", len(acquiringMap))
}

func groupData(f *excelize.File, groupingSettings []GroupingSetting) {
	rows, err := f.GetRows("Начисления")
	if err != nil {
		fmt.Printf("Ошибка при чтении листа 'Начисления': %v\n", err)
		return
	}

	if len(rows) < 3 {
		fmt.Println("Лист 'Начисления' содержит недостаточно данных")
		return
	}

	// Создаем или очищаем лист grouping
	index, err := f.GetSheetIndex("grouping")
	if err == nil && index > 0 {
		f.DeleteSheet("grouping")
	}
	_, err = f.NewSheet("grouping")
	if err != nil {
		fmt.Printf("Ошибка при создании листа 'grouping': %v\n", err)
		return
	}

	headers := rows[1]
	for i, header := range headers {
		headers[i] = cleanHeader(header)
	}
	colIndexes := make(map[string]int)
	for i, header := range headers {
		colIndexes[header] = i + 1
	}

	requiredCols := []string{"ID начисления", "Группа услуг", "Тип начисления", "Сумма итого, руб"}
	for _, col := range requiredCols {
		if _, exists := colIndexes[col]; !exists {
			fmt.Printf("Не найден обязательный столбец: %s\n", col)
			return
		}
	}

	uniqueTypes := make(map[string]bool)
	for _, row := range rows[2:] {
		if len(row) >= len(headers) {
			group := row[colIndexes["Группа услуг"]-1]
			tipNach := row[colIndexes["Тип начисления"]-1]
			if group == "Возвраты" || tipNach == "Эквайринг" {
				continue // Пропускаем возвраты и эквайринг
			}
			uniqueTypes[tipNach] = true
		}
	}

	columnMapping := make(map[string]GroupingSetting)
	for _, setting := range groupingSettings {
		columnMapping[setting.SourceColumn] = setting
	}

	for v, _ := range columnMapping {
		fmt.Println(v, "|")
	}

	finalColumns := make(map[string]bool)
	dColumns := make(map[string]bool)
	for typ := range uniqueTypes {
		if setting, exists := columnMapping[typ]; exists {
			finalColumns[setting.TargetColumn] = true
			if setting.MarkAsD {
				dColumns[setting.TargetColumn] = true
			}
		} else {
			finalColumns[typ] = true
		}
	}

	groupHeaders := []string{"Вид", "ID начисления", "Отмена"}

	var otherColumns []string
	for col := range finalColumns {
		otherColumns = append(otherColumns, col)
	}
	sort.Strings(otherColumns)
	groupHeaders = append(groupHeaders, otherColumns...)

	for i, header := range groupHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue("grouping", cell, header)
	}

	headerIndexes := make(map[string]int)
	for i, header := range groupHeaders {
		headerIndexes[header] = i + 1
	}

	type GroupData struct {
		Vid     string
		ID      string
		Otmena  float64
		Columns map[string]float64
	}

	groups := make(map[string]*GroupData)

	for _, row := range rows[2:] {
		if len(row) < len(headers) {
			continue
		}

		group := row[colIndexes["Группа услуг"]-1]
		tipNach := row[colIndexes["Тип начисления"]-1]
		if group == "Возвраты" || tipNach == "Эквайринг" || tipNach == "Возврат вознаграждения" {
			continue
		}

		id := row[colIndexes["ID начисления"]-1]
		sumStr := row[colIndexes["Сумма итого, руб"]-1]

		// Очищаем сумму
		cleanedSum := strings.Map(func(r rune) rune {
			if r == '-' || (r >= '0' && r <= '9') {
				return r
			}
			return -1
		}, sumStr)

		sum, err := strconv.ParseFloat(cleanedSum, 64)
		if err != nil {
			fmt.Printf("Ошибка парсинга суммы '%s': %v\n", sumStr, err)
			continue
		}
		sum = sum / 100

		if _, exists := groups[id]; !exists {
			groups[id] = &GroupData{
				ID:      id,
				Columns: make(map[string]float64),
			}
		}

		var targetColumn string
		var markAsD bool
		if setting, exists := columnMapping[tipNach]; exists {
			targetColumn = setting.TargetColumn
			markAsD = setting.MarkAsD
		} else {
			targetColumn = tipNach
		}

		switch {
		case tipNach == "Обработка операционных ошибок продавца: отмена":
			groups[id].Otmena += sum

		default:
			groups[id].Columns[targetColumn] += sum

			if markAsD && sum != 0 {
				groups[id].Vid = "Д"
			}
		}
	}

	// Записываем результаты и удаляем пустые столбцы
	rowNum := 2
	columnHasData := make(map[int]bool) // Для отслеживания, есть ли в столбце ненулевые значения

	// Инициализируем map для всех столбцов
	for col := 4; col <= len(groupHeaders); col++ {
		columnHasData[col] = false
	}

	for _, data := range groups {
		f.SetCellValue("grouping", fmt.Sprintf("A%d", rowNum), data.Vid)
		f.SetCellValue("grouping", fmt.Sprintf("B%d", rowNum), data.ID)
		f.SetCellValue("grouping", fmt.Sprintf("C%d", rowNum), data.Otmena)

		for colIdx, header := range groupHeaders[3:] {
			excelCol := colIdx + 4 // Столбцы начинаются с D (4)
			value := data.Columns[header]
			cell, _ := excelize.CoordinatesToCellName(excelCol, rowNum)
			f.SetCellValue("grouping", cell, value)

			// Если значение не равно 0, отмечаем что столбец содержит данные
			if value != 0 {
				columnHasData[excelCol] = true
			}
		}
		rowNum++
	}

	// Удаляем только те столбцы, где ВСЕ значения равны 0
	colsToDelete := make([]int, 0)
	for col := len(groupHeaders); col >= 4; col-- {
		if !columnHasData[col] {
			colsToDelete = append(colsToDelete, col)
		}
	}

	// Удаляем столбцы (с конца, чтобы не сбивались индексы)
	for _, col := range colsToDelete {
		colName, _ := excelize.ColumnNumberToName(col)
		f.RemoveCol("grouping", colName)
	}

	fmt.Printf("Создано %d группированных записей\n", len(groups))
}

func waitForAnyKey() {
	fmt.Println("\nНажмите любую клавишу для выхода...")
	var input [1]byte
	os.Stdin.Read(input[:])
}
