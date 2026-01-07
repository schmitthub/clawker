'use strict';

// Configuration
const DOCKERFILE_PATH = 'claude-container/Dockerfile';
const TARGETS = ['base', 'node', 'python', 'go', 'rust'];

// Files that trigger full rebuild of all images
const GLOBAL_TRIGGER_FILES = [
  'genMatrix.js',
  '.github/workflows/build-test.yml',
  'claude-container/Dockerfile',
  'claude-container/init-firewall.sh',
  'claude-container/docker-entrypoint.sh',
];

/**
 * Determines if any files require rebuilding all targets
 */
const shouldRebuildAll = (changedFiles) => {
  return changedFiles.some(file => GLOBAL_TRIGGER_FILES.includes(file));
};

/**
 * Generates build matrix based on changed files
 * Returns null if no builds needed (skips matrix entirely)
 *
 * @param {string[]} filesAdded - Array of added file paths
 * @param {string[]} filesModified - Array of modified file paths
 * @param {string[]} filesRenamed - Array of renamed file paths
 * @returns {Object|null} Matrix object with include array, or null if no builds needed
 */
const generateBuildMatrix = (filesAdded, filesModified, filesRenamed) => {
  const changedFiles = [
    ...filesAdded,
    ...filesModified,
    ...filesRenamed,
  ];

  // If no relevant files changed, skip builds
  if (changedFiles.length === 0 || !shouldRebuildAll(changedFiles)) {
    console.log('No relevant files changed, skipping builds');
    return null;
  }

  console.log('Building all targets due to changes in:', changedFiles);

  // Build all targets
  return {
    include: TARGETS.map(target => ({
      target,
      dockerfile: DOCKERFILE_PATH,
    }))
  };
};

module.exports = generateBuildMatrix;
