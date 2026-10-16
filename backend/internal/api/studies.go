package api

import (
	"context"
	"time"

	"github.com/gold-mine-lab/backend/internal/market"
)

type StudyCandidate struct {
	StrategyID     string         `json:"strategyId"`
	Parameters     map[string]any `json:"parameters,omitempty"`
	GraphRevision  *int           `json:"graphRevision,omitempty"`
	DefinitionHash string         `json:"definitionHash,omitempty"`
	Version        string         `json:"version,omitempty"`
}

type StudySpec struct {
	Candidates     []StudyCandidate `json:"candidates"`
	PrimaryMetric  string           `json:"primaryMetric"`
	PrimaryHorizon int              `json:"primaryHorizon"`
	RegimeAnalysis bool             `json:"regimeAnalysis"`
	SpecVersion    string           `json:"specVersion"`
}

type ResearchStudyRequest struct {
	Name         string           `json:"name"`
	Description  string           `json:"description"`
	Hypothesis   string           `json:"hypothesis"`
	AssetID      string           `json:"assetId"`
	Timeframe    market.Timeframe `json:"timeframe"`
	DevStart     string           `json:"devStart"`
	DevEnd       string           `json:"devEnd"`
	HoldoutStart string           `json:"holdoutStart"`
	HoldoutEnd   string           `json:"holdoutEnd"`
	Spec         StudySpec        `json:"spec"`
}

type ResearchCounts struct {
	Candidates              int    `json:"candidates"`
	ParameterConfigurations int    `json:"parameterConfigurations"`
	GraphRevisions          int    `json:"graphRevisions"`
	Horizons                int    `json:"horizons"`
	RegimeCells             int    `json:"regimeCells"`
	PriorExploratory        *int   `json:"priorExploratory,omitempty"`
	TotalComparisons        int    `json:"totalComparisons"`
	Notice                  string `json:"notice"`
}

type StudyExecution struct {
	ID               string         `json:"id"`
	Kind             string         `json:"kind"`
	Status           string         `json:"status"`
	StartedAt        time.Time      `json:"startedAt"`
	FinishedAt       time.Time      `json:"finishedAt"`
	Window           StudyWindow    `json:"window"`
	SignalResearchID *string        `json:"signalResearchId,omitempty"`
	Outcomes         []StudyOutcome `json:"outcomes"`
	Error            *string        `json:"error,omitempty"`
}

type StudyWindow struct {
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
}

type StudyOutcome struct {
	StrategyID string   `json:"strategyId"`
	Label      string   `json:"label"`
	Metric     string   `json:"metric"`
	Horizon    int      `json:"horizon"`
	Value      *float64 `json:"value"`
	CIStatus   string   `json:"ciStatus"`
	CILow      *float64 `json:"ciLow,omitempty"`
	CIHigh     *float64 `json:"ciHigh,omitempty"`
	Events     int      `json:"events"`
	Controls   int      `json:"controls"`
}

type ResearchStudy struct {
	ID                     string           `json:"id"`
	Status                 string           `json:"status"`
	Name                   string           `json:"name"`
	Description            string           `json:"description"`
	Hypothesis             string           `json:"hypothesis"`
	AssetID                string           `json:"assetId"`
	Timeframe              market.Timeframe `json:"timeframe"`
	DatasetFingerprint     string           `json:"datasetFingerprint"`
	DevStart               string           `json:"devStart"`
	DevEnd                 string           `json:"devEnd"`
	HoldoutStart           string           `json:"holdoutStart"`
	HoldoutEnd             string           `json:"holdoutEnd"`
	Spec                   StudySpec        `json:"spec"`
	SpecHash               string           `json:"specHash"`
	ResearchCounts         *ResearchCounts  `json:"researchCounts,omitempty"`
	Executions             []StudyExecution `json:"executions"`
	LastError              *string          `json:"lastError"`
	CreatedAt              string           `json:"createdAt"`
	UpdatedAt              string           `json:"updatedAt"`
	FrozenAt               *string          `json:"frozenAt"`
	ExploratoryCompletedAt *string          `json:"exploratoryCompletedAt"`
	HoldoutConsumedAt      *string          `json:"holdoutConsumedAt"`
}

type ResearchStudyService interface {
	CreateStudy(context.Context, ResearchStudyRequest) (ResearchStudy, error)
	UpdateStudy(context.Context, string, ResearchStudyRequest) (ResearchStudy, error)
	GetStudy(context.Context, string) (ResearchStudy, error)
	ListStudies(context.Context) ([]ResearchStudy, error)
	FreezeStudy(context.Context, string) (ResearchStudy, error)
	RunStudyExploratory(context.Context, string) (ResearchStudy, error)
	ValidateStudyHoldout(context.Context, string) (ResearchStudy, error)
}
