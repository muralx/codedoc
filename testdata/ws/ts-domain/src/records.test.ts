import { MemoryRepo, isHealthy } from './records.js';

it('stores and checks an environment', () => {
  const repo = new MemoryRepo();
  repo.save({ name: 'prod', health: 'ok' });
  const env = repo.load('prod');
  expect(env !== undefined && isHealthy(env)).toBe(true);
});
