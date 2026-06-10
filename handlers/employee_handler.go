package handlers

import (
	"net/http"

	"attendance_essl/people"
	"github.com/gin-gonic/gin"
)

type EmployeeHandler struct {
	manager *people.Manager
}

func NewEmployeeHandler(manager *people.Manager) *EmployeeHandler {
	return &EmployeeHandler{manager: manager}
}

type zohoPayload struct {
	ZohoID              string `json:"ZohoID"`
	FirstName           string `json:"FirstName"`
	LastName            string `json:"LastName"`
	EmailID             string `json:"EmailID"`
	Mobile              string `json:"Mobile"`
	Designation         string `json:"Designation"`
	Department          string `json:"Department"`
	ReportingToID       string `json:"ReportingToID"`
	ReportingTo         string `json:"ReportingTo"`
	SecondReportingToID string `json:"SecondReportingToID"`
	SecondReportingTo   string `json:"SecondReportingTo"`
	Photo               string `json:"Photo"`
	EmployeeID          string `json:"EmployeeID"`
	EmployeeStatus      string `json:"EmployeeStatus"`
	Team                string `json:"Team"`
	LocationName        string `json:"LocationName"`
}

func (h *EmployeeHandler) Upsert(c *gin.Context) {
	var payload zohoPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "invalid JSON: " + err.Error()})
		return
	}

	if payload.ZohoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "ZohoID is required"})
		return
	}

	emp := people.EmployeeDetail{
		ZohoID:              payload.ZohoID,
		FirstName:           payload.FirstName,
		LastName:            payload.LastName,
		EmailID:             payload.EmailID,
		Mobile:              payload.Mobile,
		Designation:         payload.Designation,
		Department:          payload.Department,
		ReportingToID:       payload.ReportingToID,
		ReportingTo:         payload.ReportingTo,
		SecondReportingToID: payload.SecondReportingToID,
		SecondReportingTo:   payload.SecondReportingTo,
		Photo:               payload.Photo,
		EmployeeID:          payload.EmployeeID,
		EmployeeStatus:      payload.EmployeeStatus,
		Team:                payload.Team,
		LocationName:        payload.LocationName,
	}

	if err := h.manager.UpsertEmployee(emp); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "employee upserted", "zoho_id": emp.ZohoID})
}
