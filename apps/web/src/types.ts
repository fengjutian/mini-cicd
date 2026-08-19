export type User = { id: string; email: string; username: string; role: string; createdAt: string }
export type StepInput = { name: string; command: string; workingDirectory?: string }
export type Project = {
  id: string; name: string; slug: string; description: string; repositoryUrl: string; branch: string;
  authType: 'none' | 'https' | 'ssh'; gitUsername: string; hasGitSecret: boolean; hasSSHPrivateKey: boolean;
  sshKnownHosts: string; buildSteps: StepInput[]; deploySteps: StepInput[]; stepTimeoutSeconds: number;
  deploymentTimeoutSeconds: number; healthEnabled: boolean; healthUrl: string; healthInitialDelaySeconds: number;
  healthTimeoutSeconds: number; healthRetries: number; healthRetryIntervalSeconds: number; healthExpectedStatus: string;
  autoDeploy: boolean; webhookProvider: string; hasWebhookSecret: boolean; createdAt: string; updatedAt: string; archivedAt?: string;
}
export type Deployment = { id: number; projectId: string; status: string; triggerType: string; branch: string; commitSha?: string; commitMessage: string; commitAuthor: string; errorSummary: string; configSource: 'project' | 'repository'; configSnapshot?: string; hasArtifacts: boolean; artifactSourceDeploymentId?: number; cacheKey?: string; cacheHit?: boolean; environment: string; approvalStatus: 'pending'|'approved'; approvedAt?: string; approvedBy?: string; queuedAt?: string; startedAt?: string; finishedAt?: string; createdAt: string }
export type DeploymentStep = { id: number; deploymentId: number; sequence: number; phase: string; name: string; command: string; workingDirectory: string; status: string; exitCode?: number; startedAt?: string; finishedAt?: string }
export type Variable = { name: string; version: number; isSecret: boolean; value?: string; updatedAt: string }
export type ApplicationResult = { adapter: string; deploymentId: number; output: string; checkedAt: string }
export type CommitStatusDelivery = { id: number; deploymentId: number; deploymentStatus: string; status: string; attempts: number; lastError: string; createdAt: string }
export type DeploymentSchedule = { id:number; name:string; environment:string; intervalMinutes:number; priority:number; cancelPrevious:boolean; parameters:Record<string,string>; enabled:boolean; nextRunAt:string; lastRunAt?:string; lastDeploymentId?:number; lastError:string }
