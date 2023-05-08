import sqlite3
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
import os
import math
import datetime
import numpy as np
import statistics
import scipy
import pprint

# function to add value labels
def addlabels(x,y):
  for i in range(len(x)):
    plt.text(i,y[i],y[i])

def mean_confidence_interval(data, confidence=0.95):
  a = 1.0 * np.array(data)
  n = len(a)
  m, se = np.mean(a), scipy.stats.sem(a)
  h = se * scipy.stats.t.ppf((1 + confidence) / 2., n-1)
  return m, m-h, m+h

# 95% confidence interval for z = 1.96
# 99% confidence interval for z = 2.576
def plot_confidence_interval(x, values, z=1.96, color='#2187bb', horizontal_line_width=0.25):
  mean = statistics.mean(values)
  stdev = statistics.stdev(values)

  confidence_interval = z * stdev / math.sqrt(len(values))

  left = x - horizontal_line_width / 2
  top = mean - confidence_interval
  right = x + horizontal_line_width / 2
  bottom = mean + confidence_interval
  plt.plot([x, x], [top, bottom], color=color)
  plt.plot([left, right], [top, top], color=color)
  plt.plot([left, right], [bottom, bottom], color=color)
  plt.plot(x, mean, 'o', color='#f44336')

  return mean, confidence_interval

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

  averages = {}

  for key, value in container_data.items():
    size_sum = 0
    count = len(value)
    for item in value:
      size_sum += item[1]
    averages[key] = (size_sum/count)

  fig = plt.figure()

  names = list(averages.keys())
  names.sort()
  values = list(averages.values())

  # Sort the averages dictionary by keys
  averages = dict(sorted(averages.items()))

  # Create a list of colors for each bar
  colors = ['b', 'g', 'r', 'c', 'm', 'y', 'k']

  # Create a list of x-coordinates that correspond to the sorted keys of the averages dictionary
  x_coords = [i for i in range(len(names))]

  # Plot the bars
  for i, container in enumerate(container_data.items()):
    data = [x[1] for x in container[1]]
    plot_confidence_interval(x_coords[i], data, color=colors.pop(0))

  # Set the x-axis tick labels to the sorted keys of the averages dictionary
  plt.xticks(x_coords, names)

  # calling the function to add value labels
  formatted_values = list(np.around(np.array(values),2))
  addlabels(names, formatted_values)

  plt.xlabel('Number of containers')
  plt.ylabel('Average checkpoint size (MB)')
  plt.title('Checkpoint sizes by number of containers')

  fig.autofmt_xdate()

  filename = "checkpoint_sizes_averages.png"
  timestamp = datetime.datetime.now().strftime("%Y-%m-%d-%H-%M-%S")
  new_filename = os.path.splitext(filename)[0] + "_" + timestamp + os.path.splitext(filename)[1]

  # Save graph image to "fig" folder
  if not os.path.exists("fig/averages"):
    os.makedirs("fig/averages")

  fig.savefig(f"fig/averages/{new_filename}")

def graph_checkpoint_time(c): 
  # Get data from database
  c.execute('SELECT timestamp, containers, size FROM checkpoint_times')
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

  averages = {}

  for key, value in container_data.items():
    size_sum = 0
    count = len(value)
    for item in value:
      size_sum += item[1]
    averages[key] = (size_sum/count)

  fig = plt.figure()

  names = list(averages.keys())
  names.sort()
  values = list(averages.values())

  # Sort the averages dictionary by keys
  averages = dict(sorted(averages.items()))

  # Create a list of colors for each bar
  colors = ['b', 'g', 'r', 'c', 'm', 'y', 'k']

  # Create a list of x-coordinates that correspond to the sorted keys of the averages dictionary
  x_coords = [i for i in range(len(names))]

  # Plot the bars
  for i, container in enumerate(container_data.items()):
    data = [x[1] for x in container[1]]
    plot_confidence_interval(x_coords[i], data, color=colors.pop(0))

  # Set the x-axis tick labels to the sorted keys of the averages dictionary
  plt.xticks(x_coords, names)

  # calling the function to add value labels
  formatted_values = list(np.around(np.array(values),2))
  addlabels(names, formatted_values)

  plt.xlabel('Number of containers')
  plt.ylabel('Average checkpoint time (ms)')
  plt.title('Checkpoint times by number of containers')

  fig.autofmt_xdate()

  filename = "checkpoint_times_averages.png"
  timestamp = datetime.datetime.now().strftime("%Y-%m-%d-%H-%M-%S")
  new_filename = os.path.splitext(filename)[0] + "_" + timestamp + os.path.splitext(filename)[1]

  # Save graph image to "fig" folder
  if not os.path.exists("fig/averages"):
    os.makedirs("fig/averages")

  fig.savefig(f"fig/averages/{new_filename}")

if __name__ == "__main__":
  print("Graphing data...")

  # Connect to database
  conn = sqlite3.connect('./db/checkpoint_data.db')
  c = conn.cursor()

  graph_checkpoint_size(c)

  graph_checkpoint_time(c)

  # Close database connection
  conn.close()