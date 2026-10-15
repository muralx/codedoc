import { MemoryRepo, isHealthy } from '@fixture/domain';

export function check(name: string): boolean {
  const repo = new MemoryRepo();
  const env = repo.load(name);
  return env !== undefined && isHealthy(env);
}
