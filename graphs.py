import sqlite3
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
import os
import pprint
import datetime
<<<<<<< HEAD
import numpy as np
import scipy
import statistics
from math import sqrt
=======
import scipy
>>>>>>> fe0ffa8 (fixup! Dividing functions for checking checkpoints)

# function to add value labels
def addlabels(x,y):
    for i in range(len(x)):
        plt.text(i,y[i],y[i])

def confidence_interval(data, confidence=0.95):
    """
    Calculate the confidence interval for a given dataset and confidence level.
    
    Args:
    - data: A list or array of numerical data.
    - confidence: A float representing the desired confidence level. Default is 0.95.
    
    Returns:
    A tuple containing the lower and upper bounds of the confidence interval.
    """
    n = len(data)
    m = np.mean(data)
    std_err = sem(data)
    h = std_err * t.ppf((1 + confidence) / 2, n - 1)
    return m - h, m + h

# function to add value labels
def addlabels(x,y):
    for i in range(len(x)):
        plt.text(i,y[i],y[i])

def graph_checkpoint_size(c):
    # Get data from database
  c.execute('SELECT timestamp, containers, size FROM checkpoint_sizes')
  data = c.fetchall()

  # Create dictionary to store data by numContainers
  container_data = {}
  for container in set([x[1] for x in data]):
      container_data[container] = []
  
  # Populate dictionary
  for row in data:
      container_data[row[1]].append((row[0], row[2]))
  
  # Calculate averages
  averages = {}
  for container, values in container_data.items():
      sizes = [v[1] for v in values]
      avg_size = sum(sizes) / len(sizes)
      averages[container] = avg_size

<<<<<<< HEAD
  averages = {}

  for key, value in container_data.items():
    size_sum = 0
    time_sum = 0
    count = len(value)
    for item in value:
        size_sum += item[1]
    averages[key] = (size_sum/count)
=======

  fig = plt.figure()
  ax = fig.add_axes([0,0,1,1])  
  ax.bar(averages.keys(), averages.values())
  ax.set_xlabel('Number of containers')
  ax.set_ylabel('Average (MB)')
  ax.set_title('Average of sizes by number of containers')
  plt.show()
>>>>>>> fe0ffa8 (fixup! Dividing functions for checking checkpoints)

  fig = plt.figure()

  names = list(averages.keys())
  names.sort()
  values = list(averages.values())

  # Sort the averages dictionary by keys
  averages = dict(sorted(averages.items()))

  # Create a list of colors for each bar
  colors = ['b', 'g', 'r', 'c', 'm', 'y', 'k']

  # Plot the bars
  for i in names:
    pprint.pprint(i)
    plt.bar(i, values[i], color=colors[i%len(colors)], align='center', width=0.7, label=f'{i} container')
    plot_confidence_interval(i, container_data[i])

  # calling the function to add value labels
  formatted_values = list(np.around(np.array(values),2))
  addlabels(names, formatted_values)

  default_x_ticks = range(len(names))
  plt.xticks(default_x_ticks, names)
  plt.xlabel('Number of containers')
  plt.ylabel('Average checkpoint size (MB)')
  plt.title('Checkpoint sizes by number of containers')
<<<<<<< HEAD

=======
>>>>>>> fe0ffa8 (fixup! Dividing functions for checking checkpoints)
  fig.autofmt_xdate()

  filename = "checkpoint_sizes_averages.png"
  timestamp = datetime.datetime.now().strftime("%Y-%m-%d-%H-%M-%S")
  new_filename = os.path.splitext(filename)[0] + "_" + timestamp + os.path.splitext(filename)[1]

  # Save graph image to "fig" folder
  if not os.path.exists("fig/averages"):
      os.makedirs("fig/averages")

  fig.savefig(f"fig/averages/{new_filename}")

  return


if __name__ == "__main__":
  print("Graphing data...")

  # Connect to database
  conn = sqlite3.connect('./db/checkpoint_data.db')
  c = conn.cursor()

  graph_checkpoint_size(c)

  # graph_checkpoint_time(c)

  # Close database connection
  conn.close()