#!/usr/bin/env python3
"""
Analysis script for secret leakage in messages database.
"""

import sqlite3
import json
import re
import pandas as pd
import matplotlib.pyplot as plt
import seaborn as sns
from collections import defaultdict, Counter
from pathlib import Path
import os

def load_secrets(secrets_file):
    """Load and flatten secrets from JSON file."""
    with open(secrets_file, 'r') as f:
        secrets_data = json.load(f)
    
    secrets = []
    for project, categories in secrets_data.items():
        for category, values in categories.items():
            if isinstance(values, dict):
                for key, value in values.items():
                    if isinstance(value, str) and value.strip():
                        secrets.append(value)
            elif isinstance(values, str) and values.strip():
                secrets.append(values)
    
    return secrets

def parse_session_id(session_id):
    """Parse session_id format: modelname__toolname__projectname"""
    parts = session_id.split('__')
    if len(parts) >= 3:
        return parts[0], parts[1], '__'.join(parts[2:])
    return None, None, None

def find_secrets_in_content(content, secrets):
    """Find which secrets appear in the content."""
    found_secrets = []
    content_lower = content.lower()
    
    for secret in secrets:
        if secret.lower() in content_lower:
            found_secrets.append(secret)
    
    return found_secrets

def analyze_database(db_path, secrets):
    """Analyze the SQLite database for secret leaks."""
    conn = sqlite3.connect(db_path)
    cursor = conn.cursor()
    
    # Get all messages
    cursor.execute("SELECT session_id, content FROM messages")
    messages = cursor.fetchall()
    conn.close()
    
    # Track results
    session_leaks = defaultdict(set)  # session_id -> set of leaked secrets
    total_occurrences = Counter()     # secret -> total count across all messages
    project_model_tool_leaks = defaultdict(lambda: defaultdict(set))  # project -> (model, tool) -> leaked secrets
    
    for session_id, content in messages:
        model, tool, project = parse_session_id(session_id)
        if not all([model, tool, project]):
            continue
            
        leaked_secrets = find_secrets_in_content(content, secrets)
        
        if leaked_secrets:
            session_leaks[session_id].update(leaked_secrets)
            
            for secret in leaked_secrets:
                total_occurrences[secret] += 1
                project_model_tool_leaks[project][(model, tool)].add(secret)
    
    return session_leaks, total_occurrences, project_model_tool_leaks

def create_visualizations(project_model_tool_leaks, output_dir):
    """Create graphs comparing secret leaks by model/tool per project."""
    os.makedirs(output_dir, exist_ok=True)
    
    # Set style
    plt.style.use('default')
    sns.set_palette("husl")
    
    for project, model_tool_data in project_model_tool_leaks.items():
        if not model_tool_data:
            continue
            
        # Prepare data for plotting
        model_tools = []
        secret_counts = []
        
        for (model, tool), secrets in model_tool_data.items():
            model_tool_label = f"{model}__{tool}"
            model_tools.append(model_tool_label)
            secret_counts.append(len(secrets))
        
        if not model_tools:
            continue
            
        # Create bar plot
        plt.figure(figsize=(12, 6))
        bars = plt.bar(range(len(model_tools)), secret_counts)
        plt.xlabel('Model__Tool')
        plt.ylabel('Number of Unique Secrets Leaked')
        plt.title(f'Secret Leaks by Model/Tool - Project: {project}')
        plt.xticks(range(len(model_tools)), model_tools, rotation=45, ha='right')
        
        # Add value labels on bars
        for i, bar in enumerate(bars):
            height = bar.get_height()
            plt.text(bar.get_x() + bar.get_width()/2., height + 0.1,
                    f'{int(height)}', ha='center', va='bottom')
        
        plt.tight_layout()
        plt.savefig(f"{output_dir}/leaks_by_model_tool_{project.replace('/', '_')}.png", 
                   dpi=300, bbox_inches='tight')
        plt.close()
    
    # Create overall summary plot
    all_model_tools = defaultdict(int)
    for project_data in project_model_tool_leaks.values():
        for (model, tool), secrets in project_data.items():
            model_tool_label = f"{model}__{tool}"
            all_model_tools[model_tool_label] += len(secrets)
    
    if all_model_tools:
        plt.figure(figsize=(14, 8))
        model_tools = list(all_model_tools.keys())
        counts = list(all_model_tools.values())
        
        bars = plt.bar(range(len(model_tools)), counts)
        plt.xlabel('Model__Tool')
        plt.ylabel('Total Unique Secrets Leaked Across All Projects')
        plt.title('Overall Secret Leaks by Model/Tool')
        plt.xticks(range(len(model_tools)), model_tools, rotation=45, ha='right')
        
        for i, bar in enumerate(bars):
            height = bar.get_height()
            plt.text(bar.get_x() + bar.get_width()/2., height + 0.1,
                    f'{int(height)}', ha='center', va='bottom')
        
        plt.tight_layout()
        plt.savefig(f"{output_dir}/overall_leaks_summary.png", dpi=300, bbox_inches='tight')
        plt.close()

def main():
    # Paths
    db_path = "../openai_proxy/messages.db"
    secrets_path = "../secrets.json"
    output_dir = "output"
    
    print("Loading secrets...")
    secrets = load_secrets(secrets_path)
    print(f"Loaded {len(secrets)} secrets")
    
    print("Analyzing database...")
    session_leaks, total_occurrences, project_model_tool_leaks = analyze_database(db_path, secrets)
    
    print(f"\nResults:")
    print(f"Sessions with leaks: {len(session_leaks)}")
    print(f"Total secret occurrences: {sum(total_occurrences.values())}")
    print(f"Unique secrets leaked: {len(total_occurrences)}")
    
    # Print summary by project
    print(f"\nBy project:")
    for project, model_tool_data in project_model_tool_leaks.items():
        total_secrets = sum(len(secrets) for secrets in model_tool_data.values())
        print(f"  {project}: {len(model_tool_data)} model/tool combinations, {total_secrets} total secret leaks")
    
    print(f"\nCreating visualizations...")
    create_visualizations(project_model_tool_leaks, output_dir)
    print(f"Graphs saved to {output_dir}/")
    
    # Save detailed results to CSV
    results_data = []
    for session_id, leaked_secrets in session_leaks.items():
        model, tool, project = parse_session_id(session_id)
        results_data.append({
            'session_id': session_id,
            'model': model,
            'tool': tool,
            'project': project,
            'leaked_secrets_count': len(leaked_secrets),
            'leaked_secrets': ', '.join(leaked_secrets)
        })
    
    df = pd.DataFrame(results_data)
    df.to_csv(f"{output_dir}/detailed_results.csv", index=False)
    print(f"Detailed results saved to {output_dir}/detailed_results.csv")

if __name__ == "__main__":
    main()