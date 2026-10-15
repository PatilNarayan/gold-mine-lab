import { apiClient } from './client';
import {
  createGraphRequestSchema, graphBacktestRequestSchema, graphDefinitionSchema, graphDiagnosticsRequestSchema,
  graphDiagnosticsSchema,
  graphRevisionSchema, strategyGraphSchema, validateGraphResponseSchema,
  backtestRunSchema,
  type CreateGraphRequest, type GraphBacktestRequest, type GraphDefinition,
  type GraphDiagnostics, type GraphDiagnosticsRequest,
  type GraphRevision, type StrategyGraph, type ValidateGraphResponse,
} from '@/types/domain';
import type { BacktestRun } from '@/types/domain';
import type { GraphService } from '../interfaces';
import { z } from 'zod';

export const httpGraphService: GraphService = {
  list(): Promise<StrategyGraph[]> {
    return apiClient.get('/api/strategy-graphs', z.array(strategyGraphSchema));
  },
  validate(definition: GraphDefinition): Promise<ValidateGraphResponse> {
    return apiClient.post('/api/strategy-graphs/validate', { definition }, validateGraphResponseSchema);
  },
  create(request: CreateGraphRequest): Promise<StrategyGraph> {
    return apiClient.post('/api/strategy-graphs', createGraphRequestSchema.parse(request), strategyGraphSchema);
  },
  get(id: string): Promise<StrategyGraph> {
    return apiClient.get(`/api/strategy-graphs/${id}`, strategyGraphSchema);
  },
  listRevisions(id: string): Promise<GraphRevision[]> {
    return apiClient.get(`/api/strategy-graphs/${id}/versions`, z.array(graphRevisionSchema));
  },
  createRevision(id: string, definition: GraphDefinition): Promise<GraphRevision> {
    return apiClient.post(`/api/strategy-graphs/${id}/versions`,
      { definition: graphDefinitionSchema.parse(definition) }, graphRevisionSchema);
  },
  runBacktest(id: string, revision: number, request: GraphBacktestRequest): Promise<BacktestRun> {
    return apiClient.post(`/api/strategy-graphs/${id}/versions/${revision}/backtests`,
      graphBacktestRequestSchema.parse(request), backtestRunSchema);
  },
  diagnostics(id: string, revision: number, request: GraphDiagnosticsRequest): Promise<GraphDiagnostics> {
    return apiClient.post(`/api/strategy-graphs/${id}/versions/${revision}/diagnostics`,
      graphDiagnosticsRequestSchema.parse(request), graphDiagnosticsSchema);
  },
};
