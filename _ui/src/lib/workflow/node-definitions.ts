export const WORKFLOW_PALETTE_GROUPS = [
  'Entry',
  'Processing',
  'Media',
  'Flow Control',
  'Resources',
  'Output',
  'Annotation',
] as const;

export type WorkflowPaletteGroup = (typeof WORKFLOW_PALETTE_GROUPS)[number];

export interface WorkflowNodeDimensions {
  width: number;
  height: number;
}

export interface WorkflowNodeDefinition {
  type: string;
  label: string;
  description: string;
  paletteGroup: WorkflowPaletteGroup | null;
  createDefaultData: () => Record<string, any>;
  dimensions?: WorkflowNodeDimensions;
}

// Keep this ordered by palette group and display order. A null group means the
// editor can render the node, but it is not available in the palette.
export const workflowNodeDefinitions = [
  {
    type: 'input',
    label: 'Input',
    description: 'Manual input data',
    paletteGroup: 'Entry',
    createDefaultData: () => ({ label: 'Input' }),
  },
  {
    type: 'http_trigger',
    label: 'HTTP Trigger',
    description: 'Webhook trigger endpoint',
    paletteGroup: null,
    createDefaultData: () => ({ label: 'HTTP Trigger', trigger_id: '', alias: '', public: false }),
  },
  {
    type: 'cron_trigger',
    label: 'Cron Trigger',
    description: 'Cron schedule trigger',
    paletteGroup: null,
    createDefaultData: () => ({ label: 'Cron Trigger', schedule: '', timezone: '', payload: {} }),
  },
  {
    type: 'llm_call',
    label: 'LLM Call',
    description: 'Call an LLM provider',
    paletteGroup: 'Processing',
    createDefaultData: () => ({ label: 'LLM Call', provider: '', model: '', system_prompt: '' }),
  },
  {
    type: 'agent_call',
    label: 'Agent Call',
    description: 'Agentic loop with tools',
    paletteGroup: 'Processing',
    createDefaultData: () => ({ label: 'Agent Call', provider: '', model: '', system_prompt: '', max_iterations: 10 }),
  },
  {
    type: 'template',
    label: 'Template',
    description: 'Template with variables',
    paletteGroup: 'Processing',
    createDefaultData: () => ({ label: 'Template', template: '', variables: [] }),
  },
  {
    type: 'workflow_call',
    label: 'Workflow Call',
    description: 'Call another workflow',
    paletteGroup: 'Processing',
    createDefaultData: () => ({ label: 'Workflow Call', workflow_id: '', workflow_name: '', inputs: {} }),
  },
  {
    type: 'http_request',
    label: 'HTTP Request',
    description: 'Make an HTTP request',
    paletteGroup: 'Processing',
    createDefaultData: () => ({
      label: 'HTTP Request',
      url: '',
      method: 'GET',
      headers: {},
      body: '',
      timeout: 30,
      proxy: '',
      insecure_skip_verify: false,
      retry: false,
    }),
  },
  {
    type: 'email',
    label: 'Email',
    description: 'Send email via SMTP',
    paletteGroup: 'Processing',
    createDefaultData: () => ({
      label: 'Email',
      config_id: '',
      to: '',
      cc: '',
      bcc: '',
      subject: '',
      body: '',
      content_type: 'text/plain',
      from: '',
      reply_to: '',
    }),
  },
  {
    type: 'script',
    label: 'Script',
    description: 'Run JavaScript code',
    paletteGroup: 'Processing',
    createDefaultData: () => ({ label: 'Script', code: '', input_count: 1 }),
  },
  {
    type: 'exec',
    label: 'Exec',
    description: 'Run a shell command',
    paletteGroup: 'Processing',
    createDefaultData: () => ({
      label: 'Exec',
      language: 'bash',
      command: '',
      working_dir: '',
      timeout: 60,
      sandbox_root: '/tmp/at-sandbox',
      input_count: 1,
    }),
  },
  {
    type: 'log',
    label: 'Log',
    description: 'Log data and pass through',
    paletteGroup: 'Processing',
    createDefaultData: () => ({ label: 'Log', level: 'info', message: '' }),
  },
  {
    type: 'image_generate',
    label: 'Image Generate',
    description: 'Generate images from text',
    paletteGroup: 'Media',
    createDefaultData: () => ({
      label: 'Image Generate',
      provider: '',
      model: '',
      size: '1024x1024',
      quality: 'standard',
      style: 'vivid',
      n: 1,
    }),
  },
  {
    type: 'vision_analyze',
    label: 'Vision Analyze',
    description: 'Analyze images with LLM',
    paletteGroup: 'Media',
    createDefaultData: () => ({ label: 'Vision Analyze', provider: '', model: '', system_prompt: '' }),
  },
  {
    type: 'audio_generate',
    label: 'Text to Speech',
    description: 'Convert text to audio',
    paletteGroup: 'Media',
    createDefaultData: () => ({
      label: 'Text to Speech',
      provider: '',
      model: 'tts-1',
      voice: 'alloy',
      response_format: 'mp3',
      speed: 1,
    }),
  },
  {
    type: 'audio_transcribe',
    label: 'Speech to Text',
    description: 'Transcribe audio to text',
    paletteGroup: 'Media',
    createDefaultData: () => ({
      label: 'Speech to Text',
      provider: '',
      model: 'whisper-1',
      language: '',
      response_format: 'json',
    }),
  },
  {
    type: 'embedding',
    label: 'Embedding',
    description: 'Create vector embeddings',
    paletteGroup: 'Media',
    createDefaultData: () => ({ label: 'Embedding', provider: '', model: '', dimensions: 0 }),
  },
  {
    type: 'conditional',
    label: 'Conditional',
    description: 'If/else branching',
    paletteGroup: 'Flow Control',
    createDefaultData: () => ({ label: 'Conditional', expression: '' }),
  },
  {
    type: 'loop',
    label: 'Loop',
    description: 'For-each fan-out',
    paletteGroup: 'Flow Control',
    createDefaultData: () => ({ label: 'Loop', expression: '' }),
  },
  {
    type: 'skill_config',
    label: 'Skill Config',
    description: 'Skills for agent nodes',
    paletteGroup: 'Resources',
    createDefaultData: () => ({ label: 'Skill Config', skills: [] }),
  },
  {
    type: 'agent_config',
    label: 'Agent Config',
    description: 'Sub-agent delegate',
    paletteGroup: 'Resources',
    createDefaultData: () => ({ label: 'Agent Config', agent_id: '' }),
  },
  {
    type: 'mcp_config',
    label: 'MCP Config',
    description: 'MCP servers for agents',
    paletteGroup: 'Resources',
    createDefaultData: () => ({ label: 'MCP Config', mcp_urls: [] }),
  },
  {
    type: 'output',
    label: 'Output',
    description: 'Workflow output data',
    paletteGroup: 'Output',
    createDefaultData: () => ({ label: 'Output', fields: [] }),
  },
  {
    type: 'group',
    label: 'Group',
    description: 'Visual grouping of nodes',
    paletteGroup: 'Annotation',
    createDefaultData: () => ({ label: 'Group', color: '#22c55e' }),
    dimensions: { width: 250, height: 200 },
  },
  {
    type: 'sticky_note',
    label: 'Sticky Note',
    description: 'Markdown note on canvas',
    paletteGroup: 'Annotation',
    createDefaultData: () => ({ text: 'Double-click to edit...', color: '#fef08a' }),
    dimensions: { width: 200, height: 140 },
  },
] as const satisfies readonly WorkflowNodeDefinition[];

export type WorkflowNodeType = (typeof workflowNodeDefinitions)[number]['type'];
export type RegisteredWorkflowNodeDefinition = (typeof workflowNodeDefinitions)[number];

export const workflowNodeDefinitionByType = Object.fromEntries(
  workflowNodeDefinitions.map((definition) => [definition.type, definition]),
) as unknown as Record<WorkflowNodeType, WorkflowNodeDefinition>;

export const workflowPaletteGroups = WORKFLOW_PALETTE_GROUPS.map((label) => ({
  label,
  nodes: workflowNodeDefinitions.filter((definition) => definition.paletteGroup === label),
}));

export const workflowNodeTypes = workflowNodeDefinitions.map((definition) => definition.type) as WorkflowNodeType[];

const workflowNodeTypeSet = new Set<string>(workflowNodeTypes);

export function isWorkflowNodeType(type: unknown): type is WorkflowNodeType {
  return typeof type === 'string' && workflowNodeTypeSet.has(type);
}

export function getWorkflowNodeDefinition(type: string): WorkflowNodeDefinition | undefined {
  return workflowNodeDefinitionByType[type as WorkflowNodeType];
}

export function getWorkflowNodeDimensions(type: string): WorkflowNodeDimensions | undefined {
  return getWorkflowNodeDefinition(type)?.dimensions;
}

export function createDefaultWorkflowNodeData(type: string): Record<string, any> {
  return getWorkflowNodeDefinition(type)?.createDefaultData() ?? {};
}

// Workflow node data is persisted as JSON. A JSON round trip provides an
// independent nested snapshot without relying on structuredClone support.
export function cloneWorkflowNodeData(data: Record<string, any>): Record<string, any> {
  return JSON.parse(JSON.stringify(data)) as Record<string, any>;
}

export function validateWorkflowNodeDefinitions(
  rendererTypes: readonly string[] = [],
  propertyTypes: readonly string[] = [],
): string[] {
  const errors: string[] = [];
  const seen = new Set<string>();
  const renderers = new Set(rendererTypes);
  const properties = new Set(propertyTypes);

  for (const registeredDefinition of workflowNodeDefinitions) {
    const definition: WorkflowNodeDefinition = registeredDefinition;
    if (seen.has(definition.type)) errors.push(`Duplicate workflow node definition: ${definition.type}`);
    seen.add(definition.type);

    const firstDefaults = definition.createDefaultData();
    const secondDefaults = definition.createDefaultData();
    if (!firstDefaults || typeof firstDefaults !== 'object' || Array.isArray(firstDefaults)) {
      errors.push(`Invalid default data for workflow node: ${definition.type}`);
    } else if (firstDefaults === secondDefaults) {
      errors.push(`Default data factory returned shared data for workflow node: ${definition.type}`);
    }

    if (definition.dimensions && (definition.dimensions.width <= 0 || definition.dimensions.height <= 0)) {
      errors.push(`Invalid dimensions for workflow node: ${definition.type}`);
    }

    if (definition.paletteGroup !== null) {
      if (rendererTypes.length > 0 && !renderers.has(definition.type)) {
        errors.push(`Missing renderer for palette workflow node: ${definition.type}`);
      }
      if (propertyTypes.length > 0 && !properties.has(definition.type)) {
        errors.push(`Missing properties component for palette workflow node: ${definition.type}`);
      }
    }
  }

  return errors;
}
